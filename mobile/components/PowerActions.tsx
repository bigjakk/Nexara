/**
 * Power-state action buttons for a VM or container. Renders a state-aware
 * set of buttons (start/shutdown/reboot/force-stop/resume) gated by the
 * `execute:vm` / `execute:container` RBAC permission.
 *
 * Hidden entirely if:
 *   - The user lacks the permission for this guest type
 *   - The guest is a template (templates can't be powered on/off via this
 *     endpoint — they need clone-to-VM first)
 *
 * Each button shows an RNAlert confirmation before firing. The destructive
 * "Force stop" variant has stronger language because it's the equivalent
 * of pulling the power and the user almost always wants graceful Shutdown
 * instead. The mutation surfaces failures via a follow-up RNAlert; success
 * is silent — the existing `useVM` polling (10s) and the `vm_state_change`
 * WS event invalidation will refresh the status pill within seconds.
 *
 * Used by:
 *   - VM detail screen (`app/(app)/clusters/[id]/vms/[vmId].tsx`) — inline
 *     in the header card
 *   - Console screen (`app/(app)/clusters/[id]/console/[vmRowId].tsx`) —
 *     inside a bottom-sheet Modal so the user can take power actions
 *     without leaving the console
 */

import {
  ActivityIndicator,
  Alert as RNAlert,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { Play, PlayCircle, Power, RotateCw, Square } from "lucide-react-native";

import {
  useGuestAction,
  type GuestAction,
  type GuestType,
} from "@/features/api/guest-action-queries";
import type { VM } from "@/features/api/types";
import { usePermissions } from "@/hooks/usePermissions";
import { useTaskTrackerStore } from "@/stores/task-tracker-store";

interface PowerActionsProps {
  vm: VM;
  clusterId: string;
  /**
   * Called immediately after the user confirms an action and the mutation
   * is dispatched (not when they just tap a button — that only opens the
   * confirm dialog). Use this to close a containing modal/sheet so the
   * user sees the underlying screen again while the action runs.
   */
  onActionFired?: () => void;
}

export function PowerActions({
  vm,
  clusterId,
  onActionFired,
}: PowerActionsProps) {
  const { canExecute } = usePermissions();
  const action = useGuestAction();

  // Is there an in-flight or recently-completed tracked task for THIS
  // specific VM? If yes, we disable the action buttons.
  //
  // We include BOTH "pending" AND "success" in the check. Here's why:
  //
  //   - **pending** covers the time between dispatch and WS event arrival.
  //     Prevents the "tap Start twice → second notification stuck forever"
  //     bug (the root cause was `task_watcher.go:62-66` silently dropping
  //     failure events for Proxmox tasks that fail on no-op actions like
  //     starting an already-running VM — see PLAN.md for details).
  //
  //   - **success** covers the race between task completion and VM query
  //     refetch. When the WS event arrives, `complete(taskId)` flips the
  //     task to success, but `useEventInvalidation` has a 300ms debounce
  //     before invalidating the VM query, then the refetch takes another
  //     ~200-500ms. During this ~500-800ms window, vm.status still shows
  //     the PRE-action state (e.g. "stopped") so the button set hasn't
  //     swapped yet. Without the success check, buttons re-enable too
  //     early and the user can fire the same action again on a stale
  //     button. Including success keeps the disable active for the full
  //     2.5-second auto-dismiss window, which is plenty of time for the
  //     refetch to finish and the button set to swap.
  //
  //   - **error** is NOT included — if an action fails, the user should
  //     be able to retry immediately without waiting for the error to
  //     auto-dismiss (which it doesn't, errors are sticky).
  //
  //   - **no_confirmation** is also NOT included — give the user agency
  //     to retry after a timeout.
  const hasActiveTaskForThisVM = useTaskTrackerStore((s) =>
    s.tasks.some(
      (t) =>
        (t.state === "pending" || t.state === "success") &&
        t.wsExpect?.vmId === vm.id,
    ),
  );

  const isContainer = vm.type === "lxc";
  const guestType: GuestType = isContainer ? "lxc" : "qemu";
  const allowed = canExecute(isContainer ? "container" : "vm");

  // Hide for templates and when the user can't act anyway. No point
  // showing disabled buttons that just clutter the screen.
  if (vm.template || !allowed) return null;

  const guestNoun = isContainer ? "container" : "VM";
  const guestLabel = vm.name || `${isContainer ? "ct" : "vm"}-${String(vm.vmid)}`;

  function trackedLabels(act: GuestAction): {
    pending: string;
    success: string;
  } {
    // Maps the action verb to a present-tense pending phrase and a
    // past-tense success phrase. Used by the global notification bar
    // so the user sees "Rebooting web-prod-01..." → "web-prod-01 rebooted".
    switch (act) {
      case "start":
        return {
          pending: `Starting ${guestLabel}`,
          success: `${guestLabel} started`,
        };
      case "stop":
        return {
          pending: `Force-stopping ${guestLabel}`,
          success: `${guestLabel} force-stopped`,
        };
      case "shutdown":
        return {
          pending: `Shutting down ${guestLabel}`,
          success: `${guestLabel} shut down`,
        };
      case "reboot":
        return {
          pending: `Rebooting ${guestLabel}`,
          success: `${guestLabel} rebooted`,
        };
      case "suspend":
        return {
          pending: `Suspending ${guestLabel}`,
          success: `${guestLabel} suspended`,
        };
      case "resume":
        return {
          pending: `Resuming ${guestLabel}`,
          success: `${guestLabel} resumed`,
        };
    }
  }

  function fire(
    act: GuestAction,
    title: string,
    body: string,
    opts?: { destructive?: boolean; confirmLabel?: string },
  ) {
    RNAlert.alert(title, body, [
      { text: "Cancel", style: "cancel" },
      {
        text: opts?.confirmLabel ?? "Confirm",
        style: opts?.destructive ? "destructive" : "default",
        onPress: () => {
          // Push a tracked task to the global notification bar BEFORE
          // dispatching the mutation. The bar shows it as pending; the
          // backend's task_watcher will publish a vm_state_change event
          // when Proxmox actually finishes, and `useTaskCompletion`
          // correlates it back to this task and flips it to success.
          const tracker = useTaskTrackerStore.getState();
          const labels = trackedLabels(act);
          const taskId = tracker.start({
            pendingLabel: labels.pending,
            successLabel: labels.success,
            wsExpect: {
              kind: "vm_state_change",
              clusterId,
              vmId: vm.id,
            },
          });
          action.mutate(
            { clusterId, vmId: vm.id, type: guestType, action: act },
            {
              onError: (err) => {
                const message =
                  err instanceof Error ? err.message : "Unknown error";
                tracker.fail(taskId, message);
                // Don't also pop an RNAlert — the notification bar IS
                // the error surface now and double-alerting is noisy.
              },
            },
          );
          // Notify the parent (e.g. close a containing bottom sheet) so
          // the user gets back to the underlying screen while the action
          // dispatches in the background.
          onActionFired?.();
        },
      },
    ]);
  }

  // Build the button list based on current power state. We deliberately
  // keep this simple in v1 — no Suspend (rare on phones) and no Reset
  // (effectively the same as Stop for the user; deferred). Add later
  // when there's a clear use case.
  type Btn = {
    id: GuestAction;
    label: string;
    icon: React.ReactNode;
    onPress: () => void;
    destructive?: boolean;
    primary?: boolean;
  };
  const buttons: Btn[] = [];

  if (vm.status === "running") {
    buttons.push({
      id: "shutdown",
      label: "Shutdown",
      icon: <Power color="#fafafa" size={18} />,
      onPress: () =>
        fire(
          "shutdown",
          `Shutdown ${guestNoun}?`,
          `Send a graceful ACPI shutdown to ${guestLabel}. The OS will shut down cleanly.`,
        ),
    });
    buttons.push({
      id: "reboot",
      label: "Reboot",
      icon: <RotateCw color="#fafafa" size={18} />,
      onPress: () =>
        fire(
          "reboot",
          `Reboot ${guestNoun}?`,
          `Send a graceful ACPI reboot to ${guestLabel}.`,
        ),
    });
    buttons.push({
      id: "stop",
      label: "Force stop",
      icon: <Square color="#ef4444" size={18} />,
      destructive: true,
      onPress: () =>
        fire(
          "stop",
          `Force-stop ${guestNoun}?`,
          `This is equivalent to pulling the power on ${guestLabel}. Unsaved data will be lost. Use Shutdown instead unless the ${guestNoun} is unresponsive.`,
          { destructive: true, confirmLabel: "Force stop" },
        ),
    });
  } else if (vm.status === "stopped" || vm.status === "unknown") {
    buttons.push({
      id: "start",
      label: "Start",
      icon: <Play color="#22c55e" size={18} />,
      primary: true,
      onPress: () =>
        fire(
          "start",
          `Start ${guestNoun}?`,
          `Power on ${guestLabel}.`,
        ),
    });
  } else if (vm.status === "paused" || vm.status === "suspended") {
    buttons.push({
      id: "resume",
      label: "Resume",
      icon: <PlayCircle color="#22c55e" size={18} />,
      primary: true,
      onPress: () =>
        fire(
          "resume",
          `Resume ${guestNoun}?`,
          `Resume ${guestLabel} from ${vm.status}.`,
        ),
    });
    buttons.push({
      id: "stop",
      label: "Force stop",
      icon: <Square color="#ef4444" size={18} />,
      destructive: true,
      onPress: () =>
        fire(
          "stop",
          `Force-stop ${guestNoun}?`,
          `Discard the ${vm.status} state of ${guestLabel} and power it off. Unsaved data will be lost.`,
          { destructive: true, confirmLabel: "Force stop" },
        ),
    });
  }

  if (buttons.length === 0) return null;

  return (
    <View className="mt-2 gap-2">
      {buttons.map((b) => {
        const isThisLoading =
          action.isPending && action.variables?.action === b.id;
        // Disable all buttons while a mutation is dispatching OR while
        // a previously-fired action's tracker task is pending/success.
        // The "success" half of the check covers the race between task
        // completion and VM query refetch — see the comment on
        // `hasActiveTaskForThisVM` above.
        const anyLoading = action.isPending || hasActiveTaskForThisVM;
        const borderClass = b.destructive
          ? "border-destructive"
          : b.primary
            ? "border-primary"
            : "border-border";
        const textClass = b.destructive
          ? "text-destructive"
          : b.primary
            ? "text-primary"
            : "text-foreground";
        return (
          <TouchableOpacity
            key={b.id}
            onPress={b.onPress}
            disabled={anyLoading}
            className={`flex-row items-center justify-center gap-2 rounded-lg border ${borderClass} py-3 ${
              anyLoading && !isThisLoading ? "opacity-40" : ""
            }`}
          >
            {isThisLoading ? (
              <ActivityIndicator
                size="small"
                color={b.destructive ? "#ef4444" : "#22c55e"}
              />
            ) : (
              b.icon
            )}
            <Text className={`text-sm font-semibold ${textClass}`}>
              {b.label}
            </Text>
          </TouchableOpacity>
        );
      })}
    </View>
  );
}
