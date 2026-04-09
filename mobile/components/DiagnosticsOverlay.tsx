/**
 * DiagnosticsOverlay — full-screen modal that shows the in-memory log buffer
 * plus the current Zustand auth-store state. Open it via long-press anywhere
 * on a screen wrapped in <DiagnosticsTrigger>.
 *
 * Designed for on-device debugging of release APKs where adb logcat is not
 * available.
 */

import { useEffect, useState } from "react";
import {
  Modal,
  Pressable,
  ScrollView,
  Text,
  TouchableOpacity,
  View,
} from "react-native";

import { logBuffer, type LogEntry } from "@/lib/log-buffer";
import { useAuthStore } from "@/stores/auth-store";

let openOverlayFn: (() => void) | null = null;

export function openDiagnostics(): void {
  if (openOverlayFn) openOverlayFn();
}

export function DiagnosticsOverlay() {
  const [visible, setVisible] = useState(false);
  const [entries, setEntries] = useState<readonly LogEntry[]>(
    logBuffer.getAll(),
  );
  const status = useAuthStore((s) => s.status);
  const serverUrl = useAuthStore((s) => s.serverUrl);
  const user = useAuthStore((s) => s.user);
  const error = useAuthStore((s) => s.error);
  const totpPending = useAuthStore((s) => s.totpPendingToken);

  useEffect(() => {
    openOverlayFn = () => setVisible(true);
    return () => {
      openOverlayFn = null;
    };
  }, []);

  useEffect(() => {
    if (!visible) return;
    setEntries(logBuffer.getAll());
    return logBuffer.subscribe(() => setEntries(logBuffer.getAll()));
  }, [visible]);

  return (
    <Modal
      visible={visible}
      animationType="slide"
      onRequestClose={() => setVisible(false)}
    >
      <View className="flex-1 bg-background">
        <View className="flex-row items-center justify-between border-b border-border bg-card p-4 pt-12">
          <Text className="text-lg font-bold text-foreground">Diagnostics</Text>
          <View className="flex-row gap-2">
            <TouchableOpacity
              className="rounded-md bg-muted px-3 py-2"
              onPress={() => {
                logBuffer.clear();
                setEntries([]);
              }}
            >
              <Text className="text-foreground">Clear</Text>
            </TouchableOpacity>
            <TouchableOpacity
              className="rounded-md bg-primary px-3 py-2"
              onPress={() => setVisible(false)}
            >
              <Text className="text-primary-foreground">Close</Text>
            </TouchableOpacity>
          </View>
        </View>

        <ScrollView className="flex-1">
          <View className="border-b border-border p-4">
            <Text className="mb-2 text-sm font-bold uppercase text-muted-foreground">
              Auth state
            </Text>
            <DiagRow label="status" value={status} />
            <DiagRow label="serverUrl" value={serverUrl ?? "(none)"} />
            <DiagRow label="user" value={user?.email ?? "(none)"} />
            <DiagRow
              label="totpPending"
              value={totpPending ? "yes" : "no"}
            />
            <DiagRow label="error" value={error ?? "(none)"} />
          </View>

          <View className="p-4">
            <Text className="mb-2 text-sm font-bold uppercase text-muted-foreground">
              Logs ({entries.length})
            </Text>
            {entries.length === 0 ? (
              <Text className="text-muted-foreground">No logs captured yet.</Text>
            ) : (
              [...entries].reverse().map((entry) => (
                <View
                  key={entry.id}
                  className="mb-2 rounded border border-border bg-card p-2"
                >
                  <Text
                    className={`text-xs ${
                      entry.level === "error"
                        ? "text-destructive"
                        : entry.level === "warn"
                          ? "text-yellow-400"
                          : "text-muted-foreground"
                    }`}
                  >
                    {new Date(entry.timestamp).toLocaleTimeString()} ·{" "}
                    {entry.level.toUpperCase()}
                  </Text>
                  <Text className="mt-1 text-xs text-foreground" selectable>
                    {entry.message}
                  </Text>
                </View>
              ))
            )}
          </View>
        </ScrollView>
      </View>
    </Modal>
  );
}

function DiagRow({ label, value }: { label: string; value: string }) {
  return (
    <View className="flex-row py-1">
      <Text className="w-28 text-xs text-muted-foreground">{label}</Text>
      <Text className="flex-1 text-xs text-foreground" selectable>
        {value}
      </Text>
    </View>
  );
}

/**
 * Wrap any screen content in this to enable long-press → open diagnostics.
 * The press doesn't interfere with normal touches because it only triggers
 * on long press.
 */
export function DiagnosticsTrigger({
  children,
}: {
  children: React.ReactNode;
}) {
  // flex: 1 is load-bearing — without it the Pressable collapses to the
  // intrinsic size of its children, and a `flex-1` SafeAreaView child
  // renders at 0 height. This happens to work on a fresh app launch
  // because expo-router's top-level Stack screen container forces its
  // child to fill, but cross-group `router.replace` navigations (e.g.
  // the "change server" flow from Settings → server-url) don't propagate
  // that fill consistently and the content ends up clipped.
  return (
    <Pressable
      style={{ flex: 1 }}
      onLongPress={() => openDiagnostics()}
      delayLongPress={600}
    >
      {children}
    </Pressable>
  );
}
