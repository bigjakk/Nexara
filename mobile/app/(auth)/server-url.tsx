import { useCallback, useEffect, useState } from "react";
import {
  Alert as RNAlert,
  KeyboardAvoidingView,
  Platform,
  ScrollView,
  Text,
  TextInput,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { X } from "lucide-react-native";

import { DiagnosticsTrigger, openDiagnostics } from "@/components/DiagnosticsOverlay";
import { secureStorage } from "@/lib/secure-storage";
import { useAuthStore } from "@/stores/auth-store";

// First-time install prefill. Always empty — self-hosted users point
// the app at their own Nexara instance, and shipping a developer's
// personal dev host as the default would either confuse new users or
// leak credentials to an unrelated server. For "change server" flows
// the screen reads the stored URL on mount and overwrites this default
// so the user can edit their existing value rather than retype it.
//
// Developers who want a per-machine prefill during local dev should
// read it from a gitignored config (e.g. app.config.local.js) rather
// than hardcoding it here.
const DEFAULT_SERVER_URL = "";

export default function ServerUrlScreen() {
  const setServerUrl = useAuthStore((s) => s.setServerUrl);
  const [url, setUrl] = useState(DEFAULT_SERVER_URL);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [recents, setRecents] = useState<string[]>([]);

  // Refresh the recent-servers list on demand. Called on mount and after
  // any mutation (add via Continue, remove via ✕ button).
  const refreshRecents = useCallback(async () => {
    const list = await secureStorage.getRecentServerUrls();
    setRecents(list);
  }, []);

  // If there's already a server URL in SecureStore (i.e. the user
  // invoked "change server" from Settings), prefill with it. First-time
  // installs see the hardcoded default above.
  useEffect(() => {
    void secureStorage.getServerUrl().then((stored) => {
      if (stored) setUrl(stored);
    });
    void refreshRecents();
  }, [refreshRecents]);

  function handleRecentTap(recent: string) {
    setUrl(recent);
    setError(null);
  }

  function handleRecentRemove(recent: string) {
    RNAlert.alert(
      "Remove from recent?",
      recent,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Remove",
          style: "destructive",
          onPress: () => {
            void secureStorage.removeRecentServerUrl(recent).then(refreshRecents);
          },
        },
      ],
    );
  }

  async function commitServerUrl(target: string) {
    console.log("[server-url] commit, url =", target);
    setError(null);
    setBusy(true);
    try {
      await setServerUrl(target);
      console.log("[server-url] setServerUrl resolved");
    } catch (err) {
      const message = err instanceof Error ? err.message : "Invalid URL";
      console.error("[server-url] setServerUrl threw:", message);
      setError(message);
    } finally {
      setBusy(false);
    }
  }

  function showSelfSignedHelp() {
    RNAlert.alert(
      "Self-signed certificate?",
      "Nexara mobile trusts certificates from any CA you've installed on this Android device. To make a self-signed or private-CA Nexara reachable:\n\n" +
        "1. Export your CA cert (or the server cert) as a .crt or .pem file.\n" +
        "2. Transfer it to your phone (email, USB, cloud, etc.).\n" +
        "3. Open Android Settings → Security → Encryption & credentials → Install a certificate → CA certificate.\n" +
        "4. Pick the file and confirm.\n" +
        "5. Come back to Nexara and try Continue again.\n\n" +
        "Once installed, the cert is trusted by Nexara automatically — no further setup needed.",
      [{ text: "Got it" }],
    );
  }

  function handleContinue() {
    const cleaned = url.trim().replace(/\/$/, "");
    // Firm warning (not a hard block) for plain HTTP — self-hosted dev/lab
    // setups legitimately run without HTTPS, so we surface the risk clearly
    // and let the user acknowledge it. HTTPS is still the strong default.
    if (/^http:\/\//i.test(cleaned)) {
      RNAlert.alert(
        "Insecure connection",
        "This URL uses plain HTTP. Your login credentials, tokens, and every API call will be sent in cleartext. Anyone on the same network can read or modify the traffic.\n\nFor real deployments, front your Nexara instance with a reverse proxy and a public-CA cert, then use the https:// URL here.\n\nContinue anyway?",
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Continue over HTTP",
            style: "destructive",
            onPress: () => {
              void commitServerUrl(cleaned);
            },
          },
        ],
      );
      return;
    }
    void commitServerUrl(cleaned);
  }

  // Recent URLs to show — excludes whatever's currently typed so the
  // user doesn't see their in-progress URL duplicated in the list.
  const visibleRecents = recents.filter(
    (r) => r.trim() !== url.trim().replace(/\/$/, ""),
  );

  return (
    <DiagnosticsTrigger>
      <SafeAreaView className="flex-1 bg-background">
        <KeyboardAvoidingView
          behavior={Platform.OS === "ios" ? "padding" : undefined}
          className="flex-1"
        >
          <ScrollView
            contentContainerStyle={{
              flexGrow: 1,
              justifyContent: "center",
              paddingHorizontal: 24,
              paddingVertical: 24,
            }}
            keyboardShouldPersistTaps="handled"
          >
            <Text className="mb-2 text-3xl font-bold text-foreground">Nexara</Text>
            <Text className="mb-8 text-muted-foreground">
              Enter the URL of your Nexara instance to continue.
            </Text>

            <TextInput
              className="rounded-lg border border-border bg-card px-4 py-3 text-foreground"
              placeholder="https://nexara.example.com"
              placeholderTextColor="#71717a"
              value={url}
              onChangeText={setUrl}
              autoCapitalize="none"
              autoCorrect={false}
              keyboardType="url"
              editable={!busy}
            />

            {error ? (
              <Text className="mt-3 text-destructive">{error}</Text>
            ) : null}

            <TouchableOpacity
              className={`mt-6 rounded-lg bg-primary py-4 ${busy ? "opacity-50" : ""}`}
              onPress={handleContinue}
              disabled={busy}
            >
              <Text className="text-center text-base font-semibold text-primary-foreground">
                {busy ? "Connecting..." : "Continue"}
              </Text>
            </TouchableOpacity>

            {/*
              Self-signed cert help link. Pops an info dialog explaining
              the Android user-CA install flow. We trust user-installed
              CAs via the network_security_config plugin (see
              mobile/plugins/with-nexara-android-network.js), but the
              user still has to actually install the cert via Android
              Settings — there's no in-app trust prompt because Expo
              managed workflow can't override TLS validation at the
              JS level. This help link is the discoverability surface
              for that out-of-band flow.
            */}
            <TouchableOpacity
              className="mt-3 py-2"
              onPress={showSelfSignedHelp}
              disabled={busy}
            >
              <Text className="text-center text-xs text-primary">
                Using a self-signed certificate?
              </Text>
            </TouchableOpacity>

            {visibleRecents.length > 0 ? (
              <View className="mt-8">
                <Text className="mb-2 px-1 text-xs font-medium uppercase text-muted-foreground">
                  Recent
                </Text>
                <View className="overflow-hidden rounded-lg border border-border bg-card">
                  {visibleRecents.map((recent, idx) => {
                    const last = idx === visibleRecents.length - 1;
                    return (
                      <View
                        key={recent}
                        className={`flex-row items-center ${
                          last ? "" : "border-b border-border"
                        }`}
                      >
                        <TouchableOpacity
                          className="flex-1 px-4 py-3"
                          onPress={() => handleRecentTap(recent)}
                          disabled={busy}
                        >
                          <Text
                            className="text-foreground"
                            numberOfLines={1}
                          >
                            {recent}
                          </Text>
                        </TouchableOpacity>
                        <TouchableOpacity
                          className="px-4 py-3"
                          onPress={() => handleRecentRemove(recent)}
                          disabled={busy}
                          hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
                        >
                          <X size={16} color="#71717a" />
                        </TouchableOpacity>
                      </View>
                    );
                  })}
                </View>
              </View>
            ) : null}

            <TouchableOpacity className="mt-8 py-2" onPress={openDiagnostics}>
              <Text className="text-center text-xs text-muted-foreground">
                Tap to open diagnostics · long-press anywhere to view logs
              </Text>
            </TouchableOpacity>
          </ScrollView>
        </KeyboardAvoidingView>
      </SafeAreaView>
    </DiagnosticsTrigger>
  );
}
