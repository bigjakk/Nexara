import { useState } from "react";
import { KeyboardAvoidingView, Platform, Text, TextInput, TouchableOpacity, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { ChevronRight, Eye, EyeOff } from "lucide-react-native";

import { maybePromptBiometricEnrollment } from "@/lib/biometric";
import { useAuthStore } from "@/stores/auth-store";

export default function LoginScreen() {
  const serverUrl = useAuthStore((s) => s.serverUrl);
  const login = useAuthStore((s) => s.login);
  const changeServer = useAuthStore((s) => s.changeServer);
  const error = useAuthStore((s) => s.error);
  const setError = useAuthStore((s) => s.setError);

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [busy, setBusy] = useState(false);

  async function handleLogin() {
    if (!email || !password) {
      setError("Email and password are required");
      return;
    }
    setError(null);
    setBusy(true);
    try {
      await login(email, password);
      // After a successful direct-to-authed login (no TOTP step), offer
      // biometric enrollment once. If login put us in totp_pending, the
      // TOTP screen will show the prompt after its own successful verify.
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
        <Text className="mb-2 text-3xl font-bold text-foreground">Sign in</Text>

        {/*
          Tappable server row. No confirm dialog — there's no active session
          to throw away on this screen, so one tap goes straight to the
          server-URL picker with the current URL prefilled and recents listed.
          This is the "I can't sign in because I'm pointed at the wrong
          server" recovery path.
        */}
        <TouchableOpacity
          className="mb-8 flex-row items-center gap-2"
          onPress={() => void changeServer()}
          disabled={busy}
          hitSlop={{ top: 8, bottom: 8 }}
        >
          <Text
            className="flex-1 text-muted-foreground"
            numberOfLines={1}
          >
            {serverUrl ?? ""}
          </Text>
          <Text className="text-xs text-primary">Change</Text>
          <ChevronRight size={14} color="#22c55e" />
        </TouchableOpacity>

        <View className="gap-3">
          <TextInput
            className="rounded-lg border border-border bg-card px-4 py-3 text-foreground"
            placeholder="Email"
            placeholderTextColor="#71717a"
            value={email}
            onChangeText={setEmail}
            autoCapitalize="none"
            autoCorrect={false}
            keyboardType="email-address"
            editable={!busy}
          />
          <View className="flex-row items-center rounded-lg border border-border bg-card">
            <TextInput
              className="flex-1 px-4 py-3 text-foreground"
              placeholder="Password"
              placeholderTextColor="#71717a"
              value={password}
              onChangeText={setPassword}
              secureTextEntry={!showPassword}
              autoCapitalize="none"
              autoCorrect={false}
              textContentType="password"
              editable={!busy}
            />
            <TouchableOpacity
              onPress={() => setShowPassword((v) => !v)}
              disabled={busy}
              className="px-4 py-3"
              hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
              accessibilityLabel={showPassword ? "Hide password" : "Show password"}
              accessibilityRole="button"
            >
              {showPassword ? (
                <EyeOff size={18} color="#71717a" />
              ) : (
                <Eye size={18} color="#71717a" />
              )}
            </TouchableOpacity>
          </View>
        </View>

        {error ? (
          <Text className="mt-3 text-destructive">{error}</Text>
        ) : null}

        <TouchableOpacity
          className={`mt-6 rounded-lg bg-primary py-4 ${busy ? "opacity-50" : ""}`}
          onPress={handleLogin}
          disabled={busy}
        >
          <Text className="text-center text-base font-semibold text-primary-foreground">
            {busy ? "Signing in..." : "Sign in"}
          </Text>
        </TouchableOpacity>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
