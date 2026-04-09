import { useState } from "react";
import { KeyboardAvoidingView, Platform, Text, TextInput, TouchableOpacity } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

import { maybePromptBiometricEnrollment } from "@/lib/biometric";
import { useAuthStore } from "@/stores/auth-store";

export default function TOTPScreen() {
  const verifyTotp = useAuthStore((s) => s.verifyTotp);
  const clearTotpPending = useAuthStore((s) => s.clearTotpPending);
  const error = useAuthStore((s) => s.error);

  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);

  async function handleSubmit() {
    if (code.length !== 6) return;
    setBusy(true);
    try {
      await verifyTotp(code);
      // Offer biometric enrollment once, right after the very first
      // successful login on a new install. Fire-and-forget — the dialog
      // overlays whatever screen the AuthGate navigates to next.
      if (useAuthStore.getState().status === "authed") {
        void maybePromptBiometricEnrollment();
      }
    } catch {
      // Error surfaced via store.error
    } finally {
      setBusy(false);
    }
  }

  return (
    <SafeAreaView className="flex-1 bg-background">
      <KeyboardAvoidingView
        behavior={Platform.OS === "ios" ? "padding" : undefined}
        className="flex-1 justify-center px-6"
      >
        <Text className="mb-2 text-3xl font-bold text-foreground">
          Two-factor code
        </Text>
        <Text className="mb-8 text-muted-foreground">
          Enter the 6-digit code from your authenticator app.
        </Text>

        <TextInput
          className="rounded-lg border border-border bg-card px-4 py-4 text-center text-2xl tracking-[8px] text-foreground"
          placeholder="000000"
          placeholderTextColor="#3f3f46"
          value={code}
          onChangeText={(v) => setCode(v.replace(/\D/g, "").slice(0, 6))}
          keyboardType="number-pad"
          maxLength={6}
          editable={!busy}
          autoFocus
        />

        {error ? (
          <Text className="mt-3 text-destructive">{error}</Text>
        ) : null}

        <TouchableOpacity
          className={`mt-6 rounded-lg bg-primary py-4 ${busy || code.length !== 6 ? "opacity-50" : ""}`}
          onPress={handleSubmit}
          disabled={busy || code.length !== 6}
        >
          <Text className="text-center text-base font-semibold text-primary-foreground">
            {busy ? "Verifying..." : "Verify"}
          </Text>
        </TouchableOpacity>

        <TouchableOpacity className="mt-3 py-2" onPress={clearTotpPending}>
          <Text className="text-center text-muted-foreground">Back to sign in</Text>
        </TouchableOpacity>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
