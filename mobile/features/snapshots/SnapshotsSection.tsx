/**
 * Snapshots section for the VM detail screen.
 *
 * Renders the existing snapshot list (sorted newest first), a "+ New"
 * button to create one, and per-row Rollback / Delete actions. All
 * actions confirm via RNAlert before firing. Restore is destructive
 * because it discards any changes since the snapshot; Delete is
 * obviously destructive too.
 *
 * Hidden for templates (snapshots are meaningless on templates) and
 * when the user lacks the underlying RBAC permission. The component
 * uses `useSnapshotPermissions(type)` to abstract over the asymmetric
 * VM-vs-CT permission model — see `snapshot-queries.ts` for the
 * mapping table.
 *
 * Mobile UX: flat list sorted by snap_time desc. The web frontend
 * shows a tree (parent-child indentation) but on a phone screen the
 * tree is more clutter than signal — users want "list, pick one,
 * restore" not "navigate the snapshot graph."
 */

import { useState } from "react";
import {
  ActivityIndicator,
  Alert as RNAlert,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { Plus, RotateCcw, Trash2 } from "lucide-react-native";

import {
  useDeleteSnapshot,
  useRollbackSnapshot,
  useSnapshotPermissions,
  useVMSnapshots,
} from "@/features/api/snapshot-queries";
import type { GuestType } from "@/features/api/guest-action-queries";
import type { Snapshot, VM } from "@/features/api/types";
import { useTaskTrackerStore } from "@/stores/task-tracker-store";

import { CreateSnapshotModal } from "./CreateSnapshotModal";

// How long to leave a snapshot task in the "pending" state before
// auto-completing. The backend publishes its event immediately on
// dispatch (no task_watcher for snapshot ops), so WS correlation would
// lie. We use a fixed delay instead so the user actually sees the
// banner before it disappears. The 30-second snapshot list polling
// will reflect the real state shortly after.
const SNAPSHOT_MIN_DISPLAY_MS = 3_500;

interface SnapshotsSectionProps {
  vm: VM;
  clusterId: string;
}

export function SnapshotsSection({ vm, clusterId }: SnapshotsSectionProps) {
  const isContainer = vm.type === "lxc";
  const guestType: GuestType = isContainer ? "lxc" : "qemu";
  const guestNoun = isContainer ? "container" : "VM";
  const guestLabel = vm.name || `${isContainer ? "ct" : "vm"}-${String(vm.vmid)}`;

  const perms = useSnapshotPermissions(guestType);
  const snapshots = useVMSnapshots(clusterId, vm.id, guestType);
  const rollback = useRollbackSnapshot();
  const remove = useDeleteSnapshot();

  const [createOpen, setCreateOpen] = useState(false);

  // Hide section entirely if the user can't even view the list. The
  // backend would 403 anyway and we'd rather not show an empty error
  // box on every VM detail screen.
  if (!perms.canList) return null;

  // Templates can technically have snapshots in Proxmox but the user
  // workflow doesn't really apply (templates are clone-source artifacts).
  // Hiding keeps the UI focused on the actually-actionable surface.
  if (vm.template) return null;

  // Sorted newest-first. snap_time may be undefined for very old or
  // weirdly-shaped snapshots — sort them last.
  const sorted = snapshots.data
    ? [...snapshots.data].sort(
        (a, b) => (b.snap_time ?? 0) - (a.snap_time ?? 0),
      )
    : [];

  function confirmRollback(s: Snapshot) {
    RNAlert.alert(
      `Restore "${s.name}"?`,
      `${guestLabel} will be reverted to this snapshot. Any changes made since the snapshot was taken will be lost.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Restore",
          style: "destructive",
          onPress: () => {
            const tracker = useTaskTrackerStore.getState();
            const taskId = tracker.start({
              pendingLabel: `Restoring snapshot "${s.name}"`,
              successLabel: `Restored snapshot "${s.name}"`,
              // No wsExpect — snapshot events fire on dispatch and would
              // lie about completion. We complete this task ourselves on
              // a fixed delay below.
            });
            rollback.mutate(
              {
                clusterId,
                vmId: vm.id,
                type: guestType,
                snapName: s.name,
              },
              {
                onSuccess: () => {
                  setTimeout(() => {
                    tracker.complete(taskId);
                  }, SNAPSHOT_MIN_DISPLAY_MS);
                },
                onError: (err) => {
                  tracker.fail(
                    taskId,
                    err instanceof Error ? err.message : "Unknown error",
                  );
                },
              },
            );
          },
        },
      ],
    );
  }

  function confirmDelete(s: Snapshot) {
    RNAlert.alert(
      `Delete "${s.name}"?`,
      `This snapshot will be permanently removed from ${guestLabel}. This cannot be undone.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: () => {
            const tracker = useTaskTrackerStore.getState();
            const taskId = tracker.start({
              pendingLabel: `Deleting snapshot "${s.name}"`,
              successLabel: `Deleted snapshot "${s.name}"`,
            });
            remove.mutate(
              {
                clusterId,
                vmId: vm.id,
                type: guestType,
                snapName: s.name,
              },
              {
                onSuccess: () => {
                  setTimeout(() => {
                    tracker.complete(taskId);
                  }, SNAPSHOT_MIN_DISPLAY_MS);
                },
                onError: (err) => {
                  tracker.fail(
                    taskId,
                    err instanceof Error ? err.message : "Unknown error",
                  );
                },
              },
            );
          },
        },
      ],
    );
  }

  // Track which row is currently in-flight so we can show a spinner
  // on just that row's action button. There can only be one mutation
  // per hook in flight at a time, so reading `variables?.snapName`
  // off the active mutation tells us which.
  const rollingBackName = rollback.isPending ? rollback.variables?.snapName : undefined;
  const deletingName = remove.isPending ? remove.variables?.snapName : undefined;
  const anyRowMutating = rollingBackName !== undefined || deletingName !== undefined;

  return (
    <>
      {/* Section header with "+ New" affordance */}
      <View className="mt-6 mb-3 flex-row items-center justify-between">
        <Text className="text-xs font-bold uppercase text-muted-foreground">
          Snapshots
        </Text>
        {perms.canCreate ? (
          <TouchableOpacity
            onPress={() => setCreateOpen(true)}
            disabled={anyRowMutating}
            className={`flex-row items-center gap-1 rounded-md border border-primary px-2 py-1 ${
              anyRowMutating ? "opacity-40" : ""
            }`}
            hitSlop={{ top: 6, bottom: 6, left: 6, right: 6 }}
          >
            <Plus color="#22c55e" size={14} />
            <Text className="text-xs font-semibold text-primary">New</Text>
          </TouchableOpacity>
        ) : null}
      </View>

      {/* List card */}
      <View className="overflow-hidden rounded-lg border border-border bg-card">
        {snapshots.isLoading && !snapshots.data ? (
          <View className="items-center p-4">
            <ActivityIndicator color="#22c55e" />
          </View>
        ) : snapshots.isError ? (
          <View className="p-4">
            <Text className="text-xs text-destructive">
              {snapshots.error instanceof Error
                ? snapshots.error.message
                : "Failed to load snapshots"}
            </Text>
          </View>
        ) : sorted.length === 0 ? (
          <View className="p-4">
            <Text className="text-xs text-muted-foreground">
              No snapshots yet. Create one before any risky change to make
              recovery a tap away.
            </Text>
          </View>
        ) : (
          sorted.map((s, idx) => {
            const last = idx === sorted.length - 1;
            const isRollingBack = rollingBackName === s.name;
            const isDeleting = deletingName === s.name;
            const rowMutating = isRollingBack || isDeleting;
            return (
              <View
                key={s.name}
                className={`p-3 ${last ? "" : "border-b border-border"} ${
                  anyRowMutating && !rowMutating ? "opacity-40" : ""
                }`}
              >
                <View className="flex-row items-start gap-2">
                  <View className="flex-1">
                    <View className="flex-row items-center gap-2">
                      <Text
                        className="flex-shrink text-sm font-semibold text-foreground"
                        numberOfLines={1}
                      >
                        {s.name}
                      </Text>
                      {s.vmstate ? (
                        <View className="rounded-sm border border-border px-1.5 py-px">
                          <Text className="text-[10px] text-muted-foreground">
                            RAM
                          </Text>
                        </View>
                      ) : null}
                    </View>
                    {s.description ? (
                      <Text
                        className="mt-0.5 text-[11px] text-muted-foreground"
                        numberOfLines={2}
                      >
                        {s.description}
                      </Text>
                    ) : null}
                    <Text className="mt-1 text-[11px] text-muted-foreground">
                      {formatSnapTime(s.snap_time)}
                    </Text>
                  </View>

                  {/* Per-row actions */}
                  <View className="flex-row items-center gap-1">
                    {perms.canRollback ? (
                      <TouchableOpacity
                        onPress={() => confirmRollback(s)}
                        disabled={anyRowMutating}
                        hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
                        className="p-2"
                      >
                        {isRollingBack ? (
                          <ActivityIndicator size="small" color="#22c55e" />
                        ) : (
                          <RotateCcw color="#22c55e" size={16} />
                        )}
                      </TouchableOpacity>
                    ) : null}
                    {perms.canDelete ? (
                      <TouchableOpacity
                        onPress={() => confirmDelete(s)}
                        disabled={anyRowMutating}
                        hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
                        className="p-2"
                      >
                        {isDeleting ? (
                          <ActivityIndicator size="small" color="#ef4444" />
                        ) : (
                          <Trash2 color="#ef4444" size={16} />
                        )}
                      </TouchableOpacity>
                    ) : null}
                  </View>
                </View>
              </View>
            );
          })
        )}
      </View>

      {/* Create snapshot modal — sibling so it's portaled out of the list */}
      <CreateSnapshotModal
        visible={createOpen}
        onClose={() => setCreateOpen(false)}
        clusterId={clusterId}
        vmId={vm.id}
        type={guestType}
        vmLabel={guestLabel}
      />
    </>
  );
}

/**
 * Format Unix-seconds snap_time into a relative-ish string. We use a
 * simple "Apr 8, 2026 16:45" format because relative timestamps are
 * less useful for snapshots — users care about *when* the snapshot was
 * taken, not "2 hours ago." Falls back to em-dash if missing.
 */
function formatSnapTime(snapTime: number | undefined): string {
  if (typeof snapTime !== "number" || !Number.isFinite(snapTime)) return "—";
  const d = new Date(snapTime * 1000);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
