import { useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert as RNAlert,
  ScrollView,
  Switch,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { ChevronRight } from "lucide-react-native";

import { getBiometricCapabilities } from "@/lib/biometric";
import { secureStorage } from "@/lib/secure-storage";
import { useAuthStore } from "@/stores/auth-store";
import {
  useDeleteDevice,
  useMyDevices,
  type MobileDevice,
} from "@/features/api/device-queries";
import { featureFlags } from "@/lib/feature-flags";
import { formatRelative } from "@/lib/format";

export default function SettingsScreen() {
  const user = useAuthStore((s) => s.user);
  const serverUrl = useAuthStore((s) => s.serverUrl);
  const logout = useAuthStore((s) => s.logout);
  const changeServer = useAuthStore((s) => s.changeServer);
  const devices = useMyDevices();
  const deleteDevice = useDeleteDevice();

  const [biometricAvailable, setBiometricAvailable] = useState(false);
  const [biometricEnrolled, setBiometricEnrolled] = useState(false);

  useEffect(() => {
    void (async () => {
      const caps = await getBiometricCapabilities();
      setBiometricAvailable(caps.hasHardware && caps.isEnrolled);
      setBiometricEnrolled(await secureStorage.isBiometricEnrolled());
    })();
  }, []);

  async function toggleBiometric(enabled: boolean) {
    await secureStorage.setBiometricEnrolled(enabled);
    setBiometricEnrolled(enabled);
  }

  function confirmChangeServer() {
    RNAlert.alert(
      "Change server URL?",
      "You'll be signed out of this server. The server URL prompt will open with your current URL prefilled for editing.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Change server",
          style: "destructive",
          onPress: () => {
            void changeServer();
          },
        },
      ],
    );
  }

  function confirmDeleteDevice(device: MobileDevice) {
    RNAlert.alert(
      "Remove device?",
      `${device.device_name} will stop receiving push notifications.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Remove",
          style: "destructive",
          onPress: () => {
            deleteDevice.mutate(device.id);
          },
        },
      ],
    );
  }

  return (
    <SafeAreaView className="flex-1 bg-background" edges={["bottom"]}>
      <ScrollView className="flex-1 p-4">
        <Section title="Account">
          <Row label="Email" value={user?.email ?? ""} />
          <Row label="Role" value={user?.role ?? ""} />
          <Row
            label="Server"
            value={serverUrl ?? ""}
            onPress={confirmChangeServer}
          />
        </Section>

        <Section title="Security">
          <View className="flex-row items-center justify-between px-4 py-3">
            <View className="flex-1">
              <Text className="text-foreground">Biometric unlock</Text>
              <Text className="text-xs text-muted-foreground">
                {biometricAvailable
                  ? "Require Face ID / fingerprint to open the app"
                  : "No biometric hardware detected"}
              </Text>
            </View>
            <Switch
              value={biometricEnrolled}
              onValueChange={toggleBiometric}
              disabled={!biometricAvailable}
            />
          </View>
        </Section>

        {featureFlags.pushNotifications && (
          <Section title="Push notifications">
            {devices.isLoading && !devices.data ? (
              <View className="px-4 py-4">
                <ActivityIndicator color="#22c55e" />
              </View>
            ) : devices.data && devices.data.length > 0 ? (
              devices.data.map((d, idx) => {
                const last = idx === (devices.data?.length ?? 0) - 1;
                return (
                  <View
                    key={d.id}
                    className={`flex-row items-center justify-between px-4 py-3 ${
                      last ? "" : "border-b border-border"
                    }`}
                  >
                    <View className="flex-1">
                      <Text className="text-foreground">{d.device_name}</Text>
                      <Text className="text-[11px] text-muted-foreground">
                        {d.platform.toUpperCase()} · seen{" "}
                        {formatRelative(d.last_seen_at)}
                      </Text>
                    </View>
                    <TouchableOpacity
                      onPress={() => confirmDeleteDevice(d)}
                      className="rounded-md border border-destructive px-3 py-1"
                    >
                      <Text className="text-xs text-destructive">Remove</Text>
                    </TouchableOpacity>
                  </View>
                );
              })
            ) : (
              <View className="px-4 py-4">
                <Text className="text-xs text-muted-foreground">
                  No devices registered yet. The app registers automatically on
                  next launch if push permission has been granted.
                </Text>
              </View>
            )}
          </Section>
        )}

        <TouchableOpacity
          className="mt-6 rounded-lg border border-destructive py-3"
          onPress={() => void logout()}
        >
          <Text className="text-center text-destructive">Sign out</Text>
        </TouchableOpacity>
      </ScrollView>
    </SafeAreaView>
  );
}

function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <View className="mb-6">
      <Text className="mb-2 px-1 text-xs font-medium uppercase text-muted-foreground">
        {title}
      </Text>
      <View className="rounded-lg border border-border bg-card">
        {children}
      </View>
    </View>
  );
}

function Row({
  label,
  value,
  onPress,
}: {
  label: string;
  value: string;
  onPress?: () => void;
}) {
  const rowClass =
    "flex-row items-center justify-between border-b border-border px-4 py-3 last:border-b-0";
  const content = (
    <>
      <Text className="text-muted-foreground">{label}</Text>
      <View className="flex-1 flex-row items-center justify-end gap-2 pl-3">
        <Text className="flex-shrink text-right text-foreground" numberOfLines={1}>
          {value}
        </Text>
        {onPress ? <ChevronRight size={16} color="#71717a" /> : null}
      </View>
    </>
  );

  if (onPress) {
    return (
      <TouchableOpacity className={rowClass} onPress={onPress}>
        {content}
      </TouchableOpacity>
    );
  }

  return <View className={rowClass}>{content}</View>;
}
