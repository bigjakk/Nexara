/**
 * Console screen — opens a WebView pointed at the web frontend's
 * `/mobile-console` route, passing a short-lived scope-locked JWT in the
 * URL. The web route renders just the noVNC or xterm viewer in a
 * full-screen layout with no app chrome.
 *
 * Route: /(app)/clusters/[id]/console/[vmRowId]
 *
 * The screen lives INSIDE the clusters Stack navigator (defined by
 * `clusters/_layout.tsx`) so back navigation works as a normal stack pop —
 * pressing back lands the user on whichever screen they came from (VM
 * detail, cluster detail, etc.) instead of bouncing all the way out to
 * the dashboard. The previous structure registered this as a hidden
 * top-level Tabs.Screen, which broke back navigation because the screen
 * wasn't in any tab's stack.
 *
 * Route params:
 *   id        — cluster UUID (from parent `clusters/[id]/`)
 *   vmRowId   — VM/CT row UUID (from this segment) — used to load the
 *               full VM data via useVM() so the power-actions menu can
 *               render and act on it
 *
 * Search params (passed via expo-router's navigation):
 *   node      — Proxmox node name
 *   vmid      — Proxmox vmid integer (omit for node_shell)
 *   type      — vm_vnc | ct_vnc | vm_serial | ct_attach | node_shell
 *   label     — display name shown in the header
 *
 * Token lifecycle:
 *   On mount, POST /api/v1/auth/console-token to mint a fresh JWT scoped
 *   to this exact (cluster_id, node, vmid, type) combination. The token
 *   has a 5-minute TTL — long enough to open the WebSocket and start the
 *   session. After that the WS stays open until the user backs out of
 *   the screen; the token is only checked once at upgrade time.
 */

import { useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  BackHandler,
  Modal,
  Pressable,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Stack, useLocalSearchParams, useRouter } from "expo-router";
import { WebView } from "react-native-webview";
import { useKeepAwake } from "expo-keep-awake";
import { ChevronLeft, Power as PowerIcon, X as XIcon } from "lucide-react-native";

import { useMintConsoleToken } from "@/features/console/console-queries";
import { useVM } from "@/features/api/vm-queries";
import { secureStorage } from "@/lib/secure-storage";
import type { ConsoleType, VMType } from "@/features/api/types";
import { PowerActions } from "@/components/PowerActions";

const VALID_TYPES: ConsoleType[] = [
  "vm_vnc",
  "ct_vnc",
  "vm_serial",
  "ct_attach",
  "node_shell",
];

function readStringParam(value: unknown): string {
  if (typeof value === "string") return value;
  if (Array.isArray(value) && typeof value[0] === "string") return value[0];
  return "";
}

export default function ConsoleScreen() {
  // Keep the screen on while the console is connected. Released on unmount.
  useKeepAwake();

  const router = useRouter();
  const params = useLocalSearchParams();

  // Cluster UUID comes from the parent route segment `clusters/[id]/`.
  // VM row UUID comes from this segment `console/[vmRowId]`.
  const clusterId = readStringParam(params.id);
  const vmRowId = readStringParam(params.vmRowId);

  const node = readStringParam(params.node);
  const typeStr = readStringParam(params.type);
  const labelRaw = readStringParam(params.label);
  const label = labelRaw || "Console";
  const vmidRaw = readStringParam(params.vmid);
  const vmidNum = useMemo(() => {
    if (!vmidRaw) return undefined;
    const n = Number.parseInt(vmidRaw, 10);
    return Number.isFinite(n) ? n : undefined;
  }, [vmidRaw]);

  const type: ConsoleType | "" = (
    VALID_TYPES as readonly string[]
  ).includes(typeStr)
    ? (typeStr as ConsoleType)
    : "";

  // Load the VM record so the power-actions menu has the data it needs.
  // The console screen can be opened for both VMs (qemu) and containers
  // (lxc); the useVM hook switches the underlying API path based on type.
  // Skipped entirely when type is `node_shell` — node shells have no VM
  // record, the `vmRowId` route param holds the node UUID as a sentinel,
  // and we don't want to accidentally hit `/clusters/:id/vms/:nodeUuid`
  // (which would 404). Passing `undefined` for the second arg disables
  // the underlying useQuery.
  const guestVMType: VMType = type === "ct_vnc" || type === "ct_attach" ? "lxc" : "qemu";
  const vm = useVM(
    clusterId,
    type === "node_shell" ? undefined : vmRowId,
    guestVMType,
  );

  const mint = useMintConsoleToken();
  const [serverUrl, setServerUrl] = useState<string | null>(null);
  const [powerSheetVisible, setPowerSheetVisible] = useState(false);

  // Resolve the configured Nexara server URL once on mount.
  useEffect(() => {
    void secureStorage.getServerUrl().then(setServerUrl);
  }, []);

  // Mint the scope-locked JWT once per unique (cluster, node, vmid, type)
  // combination. We track the last-minted params via a ref so navigating
  // from one VM's console to another always mints a fresh token instead
  // of reusing the previous VM's. The mutation's `data` field alone isn't
  // enough because it persists across screen re-renders even when the
  // route params change.
  const lastMintedKeyRef = useRef<string | null>(null);
  useEffect(() => {
    if (!serverUrl) return;
    if (!clusterId || !node || !type) return;
    if (type !== "node_shell" && vmidNum === undefined) return;

    const key = `${clusterId}|${node}|${vmidNum ?? "node"}|${type}`;
    if (lastMintedKeyRef.current === key) return;
    if (mint.isPending) return;
    lastMintedKeyRef.current = key;

    const req: {
      cluster_id: string;
      node: string;
      type: ConsoleType;
      vmid?: number;
    } = { cluster_id: clusterId, node, type };
    if (vmidNum !== undefined) {
      req.vmid = vmidNum;
    }
    console.log("[console] minting console-token for", key);
    mint.mutate(req);
    // We intentionally exclude `mint` from deps to avoid an effect loop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverUrl, clusterId, node, type, vmidNum]);

  // Hardware back button: close the WebView (which closes the WS) and pop.
  // The clusters Stack will pop one frame and land on whatever screen the
  // user came from (typically VM detail).
  useEffect(() => {
    const handler = () => {
      // If the power sheet is open, close it instead of leaving the screen.
      if (powerSheetVisible) {
        setPowerSheetVisible(false);
        return true;
      }
      router.back();
      return true;
    };
    const sub = BackHandler.addEventListener("hardwareBackPress", handler);
    return () => {
      sub.remove();
    };
  }, [router, powerSheetVisible]);

  const consoleUrl = useMemo(() => {
    if (!serverUrl || !mint.data || !type) return null;
    const params = new URLSearchParams({
      cluster_id: clusterId,
      node,
      type,
      token: mint.data.token,
      label,
    });
    if (vmidNum !== undefined) {
      params.set("vmid", String(vmidNum));
    }
    return `${serverUrl}/mobile-console?${params.toString()}`;
  }, [serverUrl, mint.data, clusterId, node, type, vmidNum, label]);

  // Origin lock for the WebView. We compute the configured server's
  // `origin` (scheme + host + port) once and refuse any navigation that
  // doesn't match it. This is the R2-H2 fix — without it, a compromised
  // or buggy backend page could navigate the WebView to an attacker-
  // controlled URL with the console token still in the current URL's
  // referer/history.
  const allowedOrigin = useMemo(() => {
    if (!serverUrl) return null;
    try {
      return new URL(serverUrl).origin;
    } catch {
      return null;
    }
  }, [serverUrl]);

  // Power menu is only meaningful for VM/CT consoles, not node shells.
  const showPowerMenu =
    type === "vm_vnc" ||
    type === "ct_vnc" ||
    type === "vm_serial" ||
    type === "ct_attach";

  return (
    <SafeAreaView className="flex-1 bg-black" edges={["top", "bottom"]}>
      <Stack.Screen
        options={{
          headerShown: false,
        }}
      />

      {/* Minimal top bar — back button, title, and (for VM/CT consoles)
          a power-menu button on the right. */}
      <View className="flex-row items-center gap-2 border-b border-border bg-card px-3 py-2">
        <TouchableOpacity
          onPress={() => router.back()}
          className="p-1"
          hitSlop={{ top: 10, bottom: 10, left: 10, right: 10 }}
        >
          <ChevronLeft color="#fafafa" size={22} />
        </TouchableOpacity>
        <Text className="flex-1 text-sm font-semibold text-foreground" numberOfLines={1}>
          {label}
        </Text>
        {showPowerMenu ? (
          <TouchableOpacity
            onPress={() => setPowerSheetVisible(true)}
            disabled={!vm.data}
            className={`p-2 ${!vm.data ? "opacity-40" : ""}`}
            hitSlop={{ top: 10, bottom: 10, left: 10, right: 10 }}
          >
            <PowerIcon color="#22c55e" size={20} />
          </TouchableOpacity>
        ) : null}
      </View>

      {/* WebView (or pre-WebView state) */}
      {!serverUrl || mint.isPending || !consoleUrl ? (
        <View className="flex-1 items-center justify-center bg-background">
          <ActivityIndicator color="#22c55e" />
          <Text className="mt-3 text-sm text-muted-foreground">
            Opening console…
          </Text>
        </View>
      ) : mint.isError ? (
        <View className="flex-1 items-center justify-center bg-background px-6">
          <Text className="mb-2 text-base font-semibold text-destructive">
            Couldn't open console
          </Text>
          <Text className="text-center text-xs text-muted-foreground">
            {mint.error instanceof Error
              ? mint.error.message
              : "Failed to mint console token"}
          </Text>
        </View>
      ) : (
        <WebView
          source={{ uri: consoleUrl }}
          className="flex-1"
          // Sane defaults for an embedded full-screen viewer
          javaScriptEnabled
          domStorageEnabled
          allowsBackForwardNavigationGestures={false}
          // Don't auto-fit / shrink — let the page handle layout
          scalesPageToFit={false}
          // Refuse HTTP subresources on an HTTPS page. The Nexara server
          // is served over TLS in both dev (via reverse proxy) and prod,
          // so there should never be a mixed-content request — if one
          // shows up it's almost certainly a misconfiguration we want to
          // surface loudly rather than silently upgrade.
          mixedContentMode="never"
          // Disable text selection long-press menu — it conflicts with VNC interaction
          textInteractionEnabled={false}
          // Block window.open / popup windows originating from the page —
          // the console view should never need them, and denying them
          // removes an escape hatch that a compromised backend could use
          // to pop an attacker-controlled URL with the token still in
          // the current URL.
          setSupportMultipleWindows={false}
          javaScriptCanOpenWindowsAutomatically={false}
          // Hide the loading state outside the WebView; we already showed
          // a spinner above while minting the token.
          startInLoadingState={false}
          // ── R2-H2: origin lock ────────────────────────────────────────
          // Only allow loads whose origin matches the configured Nexara
          // server. `originWhitelist` handles the initial load + same-
          // origin subresources; `onShouldStartLoadWithRequest` is the
          // belt-and-braces check for any top-level navigation the page
          // tries to initiate (meta-refresh, window.location =, anchor
          // clicks with target=_self, etc.). Blocked navigations return
          // `false` and the WebView stays on the current URL.
          originWhitelist={allowedOrigin ? [allowedOrigin] : []}
          onShouldStartLoadWithRequest={(req) => {
            if (!allowedOrigin) return false;
            try {
              return new URL(req.url).origin === allowedOrigin;
            } catch {
              return false;
            }
          }}
          // ── R2-H1: gate Chrome DevTools access on dev builds ─────────
          // Setting this to true calls Android's
          // WebView.setWebContentsDebuggingEnabled(true) which persists
          // for the entire app process and exposes every WebView in the
          // app to `chrome://inspect/#devices` over ADB. In release
          // builds we lock this off so an attacker with USB debugging
          // can't attach DevTools and read the console token from the
          // WebView's URL.
          webviewDebuggingEnabled={__DEV__}
          // ── R2-H3: pipe WebView console to Metro in dev builds only ──
          // The injection overrides console.log/warn/error + window error
          // handlers to postMessage back into the RN runtime, where
          // `onMessage` re-logs them through console.* so they flow into
          // our diagnostics ring buffer. This is useful for debugging
          // noVNC / xterm inside the page but it's also a leak path: if
          // the page ever logs `location.href` or throws an error whose
          // stack trace captures the URL, the console token ends up in
          // the on-device log buffer that's visible via the diagnostics
          // overlay long-press. Gate the whole thing on __DEV__ — in
          // release builds we don't forward anything from the page.
          //
          // Historical note: earlier dev builds also injected an
          // `isSecureContext` override here because the Nexara server
          // was served over plain HTTP and noVNC's WebCrypto calls would
          // fail in an insecure context. That hack is gone — dev now
          // runs behind an HTTPS reverse proxy with a publicly-trusted
          // cert, so the WebView's secure context is real and noVNC is
          // happy without any shims. If this ever stops working, the
          // first thing to check is whether the configured server URL
          // is https:// and whether its cert chains to a root the
          // system trust store knows about.
          {...(__DEV__ && {
            injectedJavaScriptBeforeContentLoaded: `
              (function() {
                var log = function(level) {
                  return function() {
                    var args = Array.prototype.slice.call(arguments).map(function(a) {
                      if (a instanceof Error) return a.stack || a.message;
                      if (typeof a === 'object') {
                        try { return JSON.stringify(a); } catch (e) { return String(a); }
                      }
                      return String(a);
                    }).join(' ');
                    try { window.ReactNativeWebView.postMessage(JSON.stringify({ level: level, msg: args })); } catch (e) {}
                  };
                };
                console.log = log('log');
                console.warn = log('warn');
                console.error = log('error');
                window.addEventListener('error', function(e) {
                  try { window.ReactNativeWebView.postMessage(JSON.stringify({ level: 'error', msg: '[window error] ' + (e.message || 'unknown') + ' @ ' + (e.filename || '?') + ':' + (e.lineno || '?') })); } catch (err) {}
                });
                window.addEventListener('unhandledrejection', function(e) {
                  try { window.ReactNativeWebView.postMessage(JSON.stringify({ level: 'error', msg: '[unhandled rejection] ' + (e.reason && e.reason.stack ? e.reason.stack : String(e.reason)) })); } catch (err) {}
                });
                true;
              })();
            `,
            onMessage: (event: { nativeEvent: { data: string } }) => {
              try {
                const data = JSON.parse(event.nativeEvent.data) as {
                  level: string;
                  msg: string;
                };
                const tag = `[webview:${data.level}]`;
                if (data.level === "error") console.error(tag, data.msg);
                else if (data.level === "warn") console.warn(tag, data.msg);
                else console.log(tag, data.msg);
              } catch {
                console.log("[webview:raw]", event.nativeEvent.data);
              }
            },
          })}
        />
      )}

      {/* Power-actions bottom sheet. Shown when the user taps the power
          icon in the top bar. Contains the same PowerActions component
          used on the VM detail screen, so RBAC gating, status-aware
          buttons, confirmation dialogs, and error surfacing all behave
          identically. We auto-dismiss the sheet when the user picks an
          action — the confirmation dialog renders on top of the closed
          sheet, which is what they want to see anyway. */}
      <Modal
        visible={powerSheetVisible}
        animationType="slide"
        transparent
        onRequestClose={() => setPowerSheetVisible(false)}
      >
        <Pressable
          className="flex-1 justify-end bg-black/60"
          onPress={() => setPowerSheetVisible(false)}
        >
          <Pressable
            // Stop the inner press from bubbling to the backdrop dismissal.
            onPress={(e) => e.stopPropagation()}
            className="rounded-t-2xl border-t border-border bg-card p-4 pb-8"
          >
            <View className="mb-3 flex-row items-center justify-between">
              <Text className="text-base font-semibold text-foreground">
                Power
              </Text>
              <TouchableOpacity
                onPress={() => setPowerSheetVisible(false)}
                hitSlop={{ top: 10, bottom: 10, left: 10, right: 10 }}
              >
                <XIcon color="#71717a" size={18} />
              </TouchableOpacity>
            </View>
            {vm.data ? (
              <PowerActions
                vm={vm.data}
                clusterId={clusterId}
                onActionFired={() => setPowerSheetVisible(false)}
              />
            ) : (
              <View className="py-4">
                <ActivityIndicator color="#22c55e" />
                <Text className="mt-2 text-center text-xs text-muted-foreground">
                  Loading VM…
                </Text>
              </View>
            )}
          </Pressable>
        </Pressable>
      </Modal>
    </SafeAreaView>
  );
}
