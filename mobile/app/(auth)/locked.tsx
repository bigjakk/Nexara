import { useEffect, useState } from "react";
import { Text, TouchableOpacity, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import {
  authenticateBiometric,
  getBiometricCapabilities,
} from "@/lib/biometric";
import { useAuthStore } from "@/stores/auth-store";

export default function LockedScreen() {
  const unlock = useAuthStore((s) => s.unlock);
  const logout = useAuthStore((s) => s.logout);
  const user = useAuthStore((s) => s.user);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void prompt();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function prompt() {
    setError(null);
    // Per security review L2: if the device's biometric enrollment was
    // REMOVED (e.g. user wiped their fingerprint in Android settings)
    // while the app was backgrounded, `authenticateBiometric` would
    // return `true` immediately because of its no-op fallback for
    // non-enrolled devices — silently unlocking the session without
    // any actual authentication. Force a logout instead so the user
    // has to re-authenticate via password.
    const caps = await getBiometricCapabilities();
    if (!caps.hasHardware || !caps.isEnrolled) {
      setError(
        "Biometric is no longer available on this device. Please sign in again.",
      );
      void logout();
      return;
    }
    const ok = await authenticateBiometric("Unlock Nexara");
    if (ok) {
      unlock();
    } else {
      setError("Authentication cancelled");
    }
  }

  return (
    <SafeAreaView className="flex-1 bg-background">
      <View className="flex-1 items-center justify-center px-6">
        <Text className="mb-2 text-3xl font-bold text-foreground">Locked</Text>
        <Text className="mb-8 text-muted-foreground">
          {user?.email ?? "Signed in"}
        </Text>

        {error ? (
          <Text className="mb-4 text-destructive">{error}</Text>
        ) : null}

        <TouchableOpacity
          className="mb-3 rounded-lg bg-primary px-8 py-4"
          onPress={prompt}
        >
          <Text className="text-base font-semibold text-primary-foreground">
            Unlock
          </Text>
        </TouchableOpacity>

        <TouchableOpacity className="py-2" onPress={() => void logout()}>
          <Text className="text-muted-foreground">Sign out</Text>
        </TouchableOpacity>
      </View>
    </SafeAreaView>
  );
}
