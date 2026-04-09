/**
 * Slide-up notification bar that surfaces tracked tasks (power actions,
 * snapshot operations, etc.) so the user gets persistent feedback while
 * an operation is running, even if they navigate away from the screen
 * that triggered it.
 *
 * Mounted once at the (app) layout level as a sibling of `<SearchModal />`.
 * Reads from `useTaskTrackerStore`. Renders nothing when there are no
 * tracked tasks.
 *
 * Layout: absolute-positioned at the bottom of the screen, above the
 * tab bar (we use `bottom: 64` which matches the default Expo router
 * Tab bar height — the safe area inset is folded in via the parent
 * SafeAreaProvider). On the console screen, the tab bar isn't shown,
 * but the absolute positioning still places the bar near the bottom of
 * the visible area, which is fine.
 *
 * Each task row:
 *   - **Pending:** spinner + pendingLabel
 *   - **Success:** green checkmark + successLabel (or pendingLabel)
 *   - **Error:** red X + pendingLabel + error message; tap to dismiss
 *   - **No confirmation** (90s timeout): muted clock icon + "<label> · still running?"
 *
 * Multiple tasks stack vertically.
 */

import {
  ActivityIndicator,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { Check, Clock, X as XIcon } from "lucide-react-native";

import { useTaskTrackerStore } from "@/stores/task-tracker-store";

export function TaskNotificationBar() {
  const tasks = useTaskTrackerStore((s) => s.tasks);
  const dismiss = useTaskTrackerStore((s) => s.dismiss);

  if (tasks.length === 0) return null;

  return (
    <View
      // Sit above the default tab bar. The (app) tabs render at ~64-80px
      // tall depending on safe area; this puts the bar just above them.
      // On screens without a tab bar (the console screen) the bar still
      // appears near the bottom which is the right place visually.
      className="absolute left-3 right-3 bottom-20 gap-2"
      pointerEvents="box-none"
    >
      {tasks.map((task) => {
        const isPending = task.state === "pending";
        const isSuccess = task.state === "success";
        const isError = task.state === "error";
        const isNoConfirmation = task.state === "no_confirmation";

        const borderClass = isError
          ? "border-destructive"
          : isSuccess
            ? "border-primary"
            : "border-border";

        const label =
          isSuccess && task.successLabel
            ? task.successLabel
            : task.pendingLabel;

        return (
          <TouchableOpacity
            key={task.id}
            // Tappable to dismiss. For pending tasks the tap is a no-op
            // (we don't let the user dismiss something that's still
            // running) — but the rest dismiss on tap.
            activeOpacity={isPending ? 1 : 0.7}
            onPress={() => {
              if (!isPending) dismiss(task.id);
            }}
            className={`flex-row items-center gap-3 rounded-lg border ${borderClass} bg-card px-3 py-3 shadow-lg`}
          >
            {isPending ? (
              <ActivityIndicator size="small" color="#22c55e" />
            ) : isSuccess ? (
              <Check color="#22c55e" size={18} />
            ) : isError ? (
              <XIcon color="#ef4444" size={18} />
            ) : isNoConfirmation ? (
              <Clock color="#71717a" size={18} />
            ) : null}

            <View className="flex-1">
              <Text
                className={`text-sm font-medium ${
                  isError ? "text-destructive" : "text-foreground"
                }`}
                numberOfLines={1}
              >
                {label}
              </Text>
              {isError && task.errorMessage ? (
                <Text
                  className="mt-0.5 text-[11px] text-destructive"
                  numberOfLines={2}
                >
                  {task.errorMessage}
                </Text>
              ) : null}
              {isNoConfirmation ? (
                <Text
                  className="mt-0.5 text-[11px] text-muted-foreground"
                  numberOfLines={1}
                >
                  Still running? Pull to refresh to verify.
                </Text>
              ) : null}
            </View>

            {!isPending ? (
              <Text className="text-[11px] text-muted-foreground">Tap</Text>
            ) : null}
          </TouchableOpacity>
        );
      })}
    </View>
  );
}
