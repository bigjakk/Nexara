/**
 * Create-snapshot bottom sheet modal.
 *
 * Slide-up Modal with name + description + (VMs only) "Include RAM"
 * toggle. Submits via `useCreateSnapshot` and dismisses on success.
 * Both success and failure are surfaced via the global TaskNotificationBar
 * (errors are sticky until tapped to dismiss).
 *
 * Validates the snapshot name client-side before submitting:
 *   - Must start with a letter
 *   - Alphanumeric + underscore + hyphen only
 *   - Max 40 characters
 * These rules mirror Proxmox's server-side enforcement (no Go validator
 * exists in the Nexara codebase yet, so we own validation here).
 *
 * Used by `SnapshotsSection`. Visibility controlled by the parent via
 * `visible` + `onClose` props.
 */

import { useState } from "react";
import {
  ActivityIndicator,
  Modal,
  Pressable,
  Switch,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from "react-native";
import { X as XIcon } from "lucide-react-native";

import {
  useCreateSnapshot,
} from "@/features/api/snapshot-queries";
import type { GuestType } from "@/features/api/guest-action-queries";
import { useTaskTrackerStore } from "@/stores/task-tracker-store";

const SNAPSHOT_NAME_RE = /^[a-zA-Z][a-zA-Z0-9_-]{0,39}$/;
const SNAPSHOT_NAME_HINT =
  "Letters, numbers, underscores, and hyphens. Must start with a letter. Max 40 characters.";

// See SnapshotsSection.tsx — snapshot ops dispatch their event before
// Proxmox finishes, so we use a fixed display delay rather than WS
// correlation to avoid the banner flashing for 100ms and disappearing.
const SNAPSHOT_MIN_DISPLAY_MS = 3_500;

interface CreateSnapshotModalProps {
  visible: boolean;
  onClose: () => void;
  clusterId: string;
  vmId: string;
  type: GuestType;
  vmLabel: string;
}

export function CreateSnapshotModal({
  visible,
  onClose,
  clusterId,
  vmId,
  type,
  vmLabel,
}: CreateSnapshotModalProps) {
  const isContainer = type === "lxc";
  const create = useCreateSnapshot();

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [includeRam, setIncludeRam] = useState(false);
  const [nameError, setNameError] = useState<string | null>(null);

  function reset() {
    setName("");
    setDescription("");
    setIncludeRam(false);
    setNameError(null);
  }

  function dismiss() {
    reset();
    onClose();
  }

  function validateName(value: string): string | null {
    if (!value) return "Snapshot name is required";
    if (!SNAPSHOT_NAME_RE.test(value)) return SNAPSHOT_NAME_HINT;
    return null;
  }

  function handleSubmit() {
    const trimmed = name.trim();
    const err = validateName(trimmed);
    if (err) {
      setNameError(err);
      return;
    }
    setNameError(null);
    // Build the request body conditionally so we don't send `undefined`
    // properties on the wire — `exactOptionalPropertyTypes: true` makes
    // TypeScript reject `description: undefined` for an optional field.
    const trimmedDescription = description.trim();
    const body: { snap_name: string; description?: string; vmstate?: boolean } = {
      snap_name: trimmed,
    };
    if (trimmedDescription) {
      body.description = trimmedDescription;
    }
    if (!isContainer) {
      // Containers ignore vmstate server-side; only send the flag for VMs.
      body.vmstate = includeRam;
    }
    // Push a tracked task to the global notification bar so the user
    // sees the operation in progress even if they navigate away from
    // this modal/screen. We dismiss the modal immediately on dispatch
    // so the user gets back to the underlying screen.
    const tracker = useTaskTrackerStore.getState();
    const taskId = tracker.start({
      pendingLabel: `Creating snapshot "${trimmed}"`,
      successLabel: `Snapshot "${trimmed}" created`,
    });
    create.mutate(
      {
        clusterId,
        vmId,
        type,
        body,
      },
      {
        onSuccess: () => {
          dismiss();
          // Hold the "creating" state visible for a moment so the user
          // sees confirmation. The 30-second snapshot list polling will
          // reflect the real state in the next refetch cycle.
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
  }

  return (
    <Modal
      visible={visible}
      animationType="slide"
      transparent
      onRequestClose={dismiss}
    >
      <Pressable
        className="flex-1 justify-end bg-black/60"
        onPress={dismiss}
      >
        <Pressable
          onPress={(e) => e.stopPropagation()}
          className="rounded-t-2xl border-t border-border bg-card p-4 pb-8"
        >
          <View className="mb-3 flex-row items-center justify-between">
            <Text className="text-base font-semibold text-foreground">
              New snapshot
            </Text>
            <TouchableOpacity
              onPress={dismiss}
              hitSlop={{ top: 10, bottom: 10, left: 10, right: 10 }}
            >
              <XIcon color="#71717a" size={18} />
            </TouchableOpacity>
          </View>

          <Text
            className="mb-3 text-xs text-muted-foreground"
            numberOfLines={1}
          >
            Snapshot of {vmLabel}
          </Text>

          {/* Name */}
          <Text className="mb-1 text-xs text-muted-foreground">Name</Text>
          <TextInput
            className="rounded-lg border border-border bg-background px-3 py-2 text-foreground"
            placeholder="pre-upgrade-2026-04-08"
            placeholderTextColor="#71717a"
            value={name}
            onChangeText={(v) => {
              setName(v);
              if (nameError) setNameError(null);
            }}
            autoCapitalize="none"
            autoCorrect={false}
            editable={!create.isPending}
            maxLength={40}
          />
          {nameError ? (
            <Text className="mt-1 text-xs text-destructive">{nameError}</Text>
          ) : (
            <Text className="mt-1 text-[11px] text-muted-foreground">
              {SNAPSHOT_NAME_HINT}
            </Text>
          )}

          {/* Description */}
          <Text className="mb-1 mt-3 text-xs text-muted-foreground">
            Description (optional)
          </Text>
          <TextInput
            className="rounded-lg border border-border bg-background px-3 py-2 text-foreground"
            placeholder="What's this snapshot for?"
            placeholderTextColor="#71717a"
            value={description}
            onChangeText={setDescription}
            multiline
            numberOfLines={2}
            editable={!create.isPending}
            style={{ textAlignVertical: "top", minHeight: 56 }}
          />

          {/* Include RAM (VMs only) */}
          {!isContainer ? (
            <View className="mt-4 flex-row items-center justify-between">
              <View className="flex-1 pr-3">
                <Text className="text-sm text-foreground">
                  Include RAM state
                </Text>
                <Text className="text-[11px] text-muted-foreground">
                  Capture memory so the VM resumes exactly where it left off.
                  Slower and uses more disk.
                </Text>
              </View>
              <Switch
                value={includeRam}
                onValueChange={setIncludeRam}
                disabled={create.isPending}
              />
            </View>
          ) : null}

          {/* Submit */}
          <TouchableOpacity
            onPress={handleSubmit}
            disabled={create.isPending}
            className={`mt-5 flex-row items-center justify-center gap-2 rounded-lg bg-primary py-3 ${
              create.isPending ? "opacity-50" : ""
            }`}
          >
            {create.isPending ? (
              <ActivityIndicator size="small" color="#0a0a0a" />
            ) : null}
            <Text className="text-sm font-semibold text-primary-foreground">
              {create.isPending ? "Creating…" : "Create snapshot"}
            </Text>
          </TouchableOpacity>
        </Pressable>
      </Pressable>
    </Modal>
  );
}
