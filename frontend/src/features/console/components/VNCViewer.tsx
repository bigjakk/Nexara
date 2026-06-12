import { useEffect, useRef, useState } from "react";
import RFB from "@novnc/novnc";
import {
  AlertCircle,
  Loader2,
  Play,
  PowerOff,
  RotateCcw,
  Unplug,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  MAX_CONSOLE_AUTO_RETRIES,
  type ConsoleStatus,
  type ConsoleTab,
} from "../types/console";
import { useConsoleStore } from "@/stores/console-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { useVMAction } from "@/features/vms/api/vm-queries";
import { useGuestPowerSync } from "../hooks/useGuestPowerSync";
import { VNCToolbar } from "./VNCToolbar";
import {
  buildVncWsUrl,
  mintConsoleToken,
  wsAuthProtocols,
} from "../api/console-queries";

interface VNCViewerProps {
  tab: ConsoleTab;
  visible: boolean;
  /**
   * Optional pre-minted scoped console token. When provided, the component
   * skips the inline mint and uses this token directly (mobile passes a
   * token minted upstream by its native shell). When omitted, the desktop
   * flow mints via POST /api/v1/auth/console-token before opening the WS.
   *
   * Either way the token rides in `Sec-WebSocket-Protocol` (per remediation
   * 2.7) — never in the URL — so it's not exposed in proxy access logs or
   * Referer headers. The /ws/vnc endpoint rejects regular access tokens
   * (per-cluster RBAC enforcement, security fix #1).
   */
  accessToken?: string;
}

/**
 * Type out text as individual key events into a VNC session.
 * Converts each character to an X11 keysym and sends press/release.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function typeTextIntoVnc(rfb: RFB, text: string) {
  for (const ch of text) {
    const code = ch.codePointAt(0);
    if (code === undefined) continue;

    let keysym: number;
    if (ch === "\n" || ch === "\r") {
      keysym = 0xff0d; // XK_Return
    } else if (ch === "\t") {
      keysym = 0xff09; // XK_Tab
    } else if (code <= 0x00ff) {
      // Latin-1: keysym === unicode code point
      keysym = code;
    } else {
      // Unicode above Latin-1: keysym = 0x01000000 + code point
      keysym = 0x01000000 + code;
    }

    rfb.sendKey(keysym, null, true);  // key down
    rfb.sendKey(keysym, null, false); // key up
  }
}

export function VNCViewer({ tab, visible, accessToken }: VNCViewerProps) {
  const { id: tabId, clusterID, node, vmid, reconnectKey } = tab;
  const containerRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<RFB | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const updateTabStatus = useConsoleStore((s) => s.updateTabStatus);
  const resolveAndReconnect = useConsoleStore((s) => s.resolveAndReconnect);
  const [rfb, setRfb] = useState<RFB | null>(null);
  const retryCountRef = useRef(0);
  const retryScheduledRef = useRef(false);
  const intentionalCloseRef = useRef(false);
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // The mobile page (/mobile-console) passes a synthetic tab that does NOT
  // live in the console store, so store-based status updates are no-ops
  // there. Mirror the connection status (and a manual reconnect key) in
  // local state so the overlay and retries work for storeless tabs too.
  const [localStatus, setLocalStatus] = useState<ConsoleStatus>("connecting");
  const localStatusRef = useRef<ConsoleStatus>("connecting");
  const [localReconnectKey, setLocalReconnectKey] = useState(0);

  // Park dead tabs while the guest is off; auto-resume when it powers on.
  useGuestPowerSync(tab);

  // Store latest callbacks in refs so the effect doesn't depend on them.
  const resolveAndReconnectRef = useRef(resolveAndReconnect);
  resolveAndReconnectRef.current = resolveAndReconnect;
  const tabIdRef = useRef(tabId);
  tabIdRef.current = tabId;

  const applyStatus = (status: ConsoleStatus) => {
    updateTabStatus(tabId, status); // no-op for storeless (mobile) tabs
    localStatusRef.current = status;
    setLocalStatus(status);
  };
  const applyStatusRef = useRef(applyStatus);
  applyStatusRef.current = applyStatus;

  const guestType = tab.type === "ct_vnc" ? "lxc" : undefined;

  useEffect(() => {
    intentionalCloseRef.current = false;
    retryScheduledRef.current = false;
    let ws: WebSocket | null = null;
    let stateLog1Timer: ReturnType<typeof setTimeout> | null = null;
    let stateLog2Timer: ReturnType<typeof setTimeout> | null = null;

    // Storeless (mobile) tabs fall back to the local status mirror.
    const tabIsParked = () =>
      (useConsoleStore.getState().tabs.find((t) => t.id === tabIdRef.current)
        ?.status ?? localStatusRef.current) === "guest-stopped";

    // Single funnel for auto-reconnects. Both the RFB disconnect event and
    // ws.onclose fire for one drop — without the dedup flag they each
    // scheduled a timer (and the second overwrote retryTimerRef, leaking the
    // first), producing overlapping reconnect cycles.
    const scheduleRetry = () => {
      if (intentionalCloseRef.current || retryScheduledRef.current) return;
      if (tabIsParked()) return; // guest is off — wait for power-on instead
      if (retryCountRef.current < MAX_CONSOLE_AUTO_RETRIES) {
        const delay = Math.min(1000 * 2 ** retryCountRef.current, 10000);
        retryCountRef.current++;
        retryScheduledRef.current = true;
        applyStatusRef.current("reconnecting");
        retryTimerRef.current = setTimeout(() => {
          retryScheduledRef.current = false;
          const inStore = useConsoleStore
            .getState()
            .tabs.some((t) => t.id === tabIdRef.current);
          if (inStore) {
            void resolveAndReconnectRef.current(tabIdRef.current);
          } else {
            // Storeless (mobile) tab — reconnect via the local key.
            applyStatusRef.current("connecting");
            setLocalReconnectKey((k) => k + 1);
          }
        }, delay);
      } else {
        applyStatusRef.current("disconnected");
      }
    };

    const connect = async () => {
      // Remounted (e.g. minimize/restore) while parked on a stopped guest —
      // don't reopen a connection that's known to fail; useGuestPowerSync
      // resumes the tab when the guest powers on.
      if (tabIsParked()) return;

      // Acquire the WS upgrade token. Desktop callers omit accessToken and
      // mint a short-lived scoped JWT; mobile passes its pre-minted token
      // through the prop.
      let token: string;
      try {
        if (accessToken) {
          token = accessToken;
        } else {
          // VNC scope type matches the tab type directly here — Terminal
          // uses node_shell/vm_serial/ct_attach, VNCViewer uses
          // vm_vnc/ct_vnc. The VNC subset is what tab.type can hold for
          // this component.
          const minted = await mintConsoleToken({
            clusterId: clusterID,
            node,
            type: tab.type,
            ...(vmid !== undefined ? { vmid } : {}),
          });
          token = minted.token;
        }
      } catch (err) {
        if (intentionalCloseRef.current) return;
        console.error("[VNCViewer] failed to mint console token", err);
        applyStatusRef.current("error");
        return;
      }

      if (intentionalCloseRef.current) return;

      const wsUrl = buildVncWsUrl(clusterID, node, vmid, guestType);
      // The wsUrl is now token-free (token rides in subprotocol). Log it.
      console.log(
        "[VNCViewer] opening WS",
        wsUrl,
        JSON.stringify({ clusterID, node, vmid, guestType }),
      );
      ws = new WebSocket(wsUrl, wsAuthProtocols(token));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      const localWs = ws; // narrow non-null binding for closures

      localWs.onopen = () => {
        console.log("[VNCViewer] WS open, readyState:", localWs.readyState);
      };

      // Diagnostic: log readyState 1 second and 5 seconds after creation in
      // case onopen / onerror / onclose never fire (silent failure mode).
      stateLog1Timer = setTimeout(() => {
        console.log(
          "[VNCViewer] WS state @ 1s",
          "readyState:", localWs.readyState,
          "(0=connecting, 1=open, 2=closing, 3=closed)",
        );
      }, 1000);
      stateLog2Timer = setTimeout(() => {
        console.log("[VNCViewer] WS state @ 5s readyState:", localWs.readyState);
      }, 5000);

      localWs.onmessage = (event: MessageEvent) => {
        if (typeof event.data === "string") {
          try {
            const msg = JSON.parse(event.data) as {
              type: string;
              code?: string;
              message?: string;
              password?: string;
            };
            if (msg.type === "connected") {
              // Backend proxy is connected to Proxmox — now initialize noVNC RFB.
              console.log("[VNCViewer] received connected, container:", !!containerRef.current);
              if (!containerRef.current) {
                console.error("[VNCViewer] containerRef is null at connected time");
                return;
              }

              retryCountRef.current = 0;

              const options: Record<string, unknown> = {};
              if (msg.password) {
                options["credentials"] = { password: msg.password };
              }

              const rfbInstance = new RFB(containerRef.current, localWs, options);
              rfbInstance.scaleViewport = true;
              rfbInstance.resizeSession = false;
              rfbInstance.focusOnClick = true;

              rfbInstance.addEventListener("connect", () => {
                console.log("[VNCViewer] RFB connect event fired");
                applyStatusRef.current("connected");
              });

              rfbInstance.addEventListener("disconnect", () => {
                console.log("[VNCViewer] RFB disconnect event fired");
                if (intentionalCloseRef.current) {
                  applyStatusRef.current("disconnected");
                  rfbRef.current = null;
                  setRfb(null);
                  return;
                }

                rfbRef.current = null;
                setRfb(null);
                scheduleRetry();
              });

              rfbInstance.addEventListener("securityfailure", () => {
                applyStatusRef.current("error");
              });

              rfbRef.current = rfbInstance;
              setRfb(rfbInstance);
              return;
            }
            if (msg.type === "error") {
              if (msg.code === "guest_not_running") {
                // Backend confirmed the guest is powered off — park with a
                // fresh retry budget for when it comes back.
                retryCountRef.current = 0;
                applyStatusRef.current("guest-stopped");
              } else {
                applyStatusRef.current("error");
              }
              return;
            }
          } catch {
            // Not JSON — ignore
          }
        }
      };

      localWs.onclose = (event) => {
        console.log(
          "[VNCViewer] WS close",
          "code:", event.code,
          "reason:", event.reason || "(none)",
          "wasClean:", event.wasClean,
          "readyState:", localWs.readyState,
        );
        if (intentionalCloseRef.current) return;
        if (!rfbRef.current) {
          // WS closed before RFB was established — auto-reconnect
          scheduleRetry();
        }
      };

      localWs.onerror = (event) => {
        console.error(
          "[VNCViewer] WS error event",
          "type:", event.type,
          "readyState:", localWs.readyState,
        );
        if (!intentionalCloseRef.current && !tabIsParked()) {
          applyStatusRef.current("error");
        }
      };
    };

    void connect();

    return () => {
      intentionalCloseRef.current = true;
      clearTimeout(retryTimerRef.current);
      retryScheduledRef.current = false;
      if (stateLog1Timer) clearTimeout(stateLog1Timer);
      if (stateLog2Timer) clearTimeout(stateLog2Timer);
      if (rfbRef.current) {
        rfbRef.current.disconnect();
        rfbRef.current = null;
        setRfb(null);
      } else {
        ws?.close();
      }
      wsRef.current = null;
    };
    // Only re-run when the actual connection parameters change.
  }, [tabId, tab.type, clusterID, node, vmid, guestType, reconnectKey, localReconnectKey, accessToken]);

  const isMinimized = useConsoleStore((s) => s.windowMode) === "minimized";

  // Mobile mode: activated when an accessToken is passed (i.e. the
  // /mobile-console route from the React Native WebView). In this mode we
  // hide the desktop toolbar, render a hidden focusable input that brings
  // up the soft keyboard when focused, and forward keystrokes from that
  // input to noVNC's RFB.sendKey().
  const isMobile = !!accessToken;
  const mobileInputRef = useRef<HTMLInputElement>(null);

  function focusMobileKeyboard() {
    mobileInputRef.current?.focus();
  }

  function handleMobileKeyEvent(
    e: React.KeyboardEvent<HTMLInputElement>,
    down: boolean,
  ) {
    if (!rfbRef.current) return;
    const keysym = mapBrowserKeyToKeysym(e);
    if (keysym !== null) {
      rfbRef.current.sendKey(keysym, e.code || null, down);
      // Prevent the input from actually receiving characters — we don't
      // want it to display anything, only act as a keyboard host.
      e.preventDefault();
    }
  }

  // Desktop tabs live in the store, so tab.status is authoritative (and is
  // what external park/resume updates touch). Mobile's synthetic tab is
  // static — use the local mirror there.
  const effectiveStatus = isMobile ? localStatus : tab.status;

  function handleManualReconnect() {
    if (isMobile) {
      applyStatusRef.current("connecting");
      setLocalReconnectKey((k) => k + 1);
    } else {
      useConsoleStore.getState().reconnectTab(tabId);
    }
  }

  return (
    <div
      className="flex h-full flex-col"
      style={{ display: visible ? "flex" : "none" }}
    >
      {!isMobile && !isMinimized && <VNCToolbar rfb={rfb} tab={tab} />}
      <div className="relative flex-1 overflow-hidden">
        <div
          ref={containerRef}
          className="h-full w-full bg-black"
          data-tab-id={tab.id}
          onClick={isMobile ? focusMobileKeyboard : undefined}
        />
        <ConsoleStateOverlay
          tab={tab}
          status={effectiveStatus}
          onReconnect={handleManualReconnect}
        />
      </div>
      {isMobile && (
        <>
          {/* Hidden input that holds the soft-keyboard focus. Positioned
              off-screen so the user never sees it but the OS treats it as
              an active text field. */}
          <input
            ref={mobileInputRef}
            type="text"
            autoCapitalize="off"
            autoCorrect="off"
            autoComplete="off"
            spellCheck={false}
            value=""
            onChange={() => {
              // We never accumulate value — keystrokes are forwarded to VNC.
            }}
            onKeyDown={(e) => { handleMobileKeyEvent(e, true); }}
            onKeyUp={(e) => { handleMobileKeyEvent(e, false); }}
            style={{
              position: "absolute",
              left: -9999,
              top: 0,
              width: 1,
              height: 1,
              opacity: 0,
              pointerEvents: "none",
            }}
          />
          {/* Floating bottom toolbar with key combos + show-keyboard. */}
          <MobileConsoleToolbar
            rfb={rfb}
            onShowKeyboard={focusMobileKeyboard}
          />
        </>
      )}
    </div>
  );
}

/**
 * Centered overlay communicating the console's connection state. Replaces
 * the old behavior of a bare black canvas for every non-connected state.
 * The "guest-stopped" state is the parked powered-off console: it offers a
 * Start button and notes that the console resumes on power-on (driven by
 * useGuestPowerSync in the parent).
 */
function ConsoleStateOverlay({
  tab,
  status,
  onReconnect,
}: {
  tab: ConsoleTab;
  /** Effective status — store-backed on desktop, local mirror on mobile. */
  status: ConsoleStatus;
  onReconnect: () => void;
}) {
  const actionMutation = useVMAction();
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  if (status === "connected") return null;

  const isCt = tab.kind === "ct" || tab.type === "ct_vnc";
  const kindLabel = isCt ? "Container" : "VM";
  const hasResource = tab.resourceId !== undefined && tab.kind !== undefined;

  function handleStart() {
    if (!tab.resourceId || !tab.kind) return;
    const guestName = tab.label.split(": ")[1] ?? "";
    actionMutation.mutate(
      {
        clusterId: tab.clusterID,
        resourceId: tab.resourceId,
        kind: tab.kind,
        action: "start",
      },
      {
        onSuccess: (data) => {
          setFocusedTask({
            clusterId: tab.clusterID,
            upid: data.upid,
            description: `Start ${guestName}`.trim(),
          });
          setPanelOpen(true);
        },
      },
    );
  }

  if (status === "connecting" || status === "reconnecting") {
    return (
      <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-2 bg-black/60 text-muted-foreground">
        <Loader2 className="h-6 w-6 animate-spin" />
        <p className="text-xs">
          {status === "connecting" ? "Connecting…" : "Reconnecting…"}
        </p>
      </div>
    );
  }

  if (status === "guest-stopped") {
    return (
      <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-black/80 px-4 text-center">
        <PowerOff className="h-8 w-8 text-muted-foreground" />
        <div>
          <p className="text-sm font-medium text-foreground">
            {kindLabel} is powered off
          </p>
          {hasResource && (
            <p className="mt-1 text-xs text-muted-foreground">
              The console will connect automatically when it powers on.
            </p>
          )}
        </div>
        <div className="flex flex-wrap items-center justify-center gap-2">
          {hasResource && (
            <Button
              size="sm"
              className="h-7 gap-1.5 px-3 text-xs"
              disabled={actionMutation.isPending}
              onClick={handleStart}
            >
              {actionMutation.isPending ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Play className="h-3.5 w-3.5" />
              )}
              {actionMutation.isPending ? "Starting…" : `Start ${kindLabel}`}
            </Button>
          )}
          <Button
            variant="outline"
            size="sm"
            className="h-7 gap-1.5 px-3 text-xs"
            onClick={onReconnect}
          >
            <RotateCcw className="h-3.5 w-3.5" />
            Connect anyway
          </Button>
        </div>
      </div>
    );
  }

  // disconnected / error
  const isError = status === "error";
  return (
    <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-black/80 px-4 text-center">
      {isError ? (
        <AlertCircle className="h-8 w-8 text-destructive" />
      ) : (
        <Unplug className="h-8 w-8 text-muted-foreground" />
      )}
      <div>
        <p className="text-sm font-medium text-foreground">
          {isError ? "Console connection failed" : "Console disconnected"}
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          {isError
            ? "The console session could not be established."
            : "Automatic reconnect attempts were exhausted."}
        </p>
      </div>
      <Button
        variant="outline"
        size="sm"
        className="h-7 gap-1.5 px-3 text-xs"
        onClick={onReconnect}
      >
        <RotateCcw className="h-3.5 w-3.5" />
        {isError ? "Retry" : "Reconnect"}
      </Button>
    </div>
  );
}

/**
 * Mobile-only floating toolbar with the most-used key combos and a button
 * to bring up the soft keyboard.
 */
function MobileConsoleToolbar({
  rfb,
  onShowKeyboard,
}: {
  rfb: RFB | null;
  onShowKeyboard: () => void;
}) {
  function sendCtrlAltDel() {
    if (!rfb) return;
    rfb.sendCtrlAltDel();
  }
  function sendKey(keysym: number, label: string) {
    if (!rfb) return;
    rfb.sendKey(keysym, label, true);
    rfb.sendKey(keysym, label, false);
  }
  const disabled = !rfb;
  const btnStyle: React.CSSProperties = {
    background: "rgba(34, 197, 94, 0.15)",
    border: "1px solid rgba(34, 197, 94, 0.4)",
    color: "#10b981",
    padding: "6px 10px",
    borderRadius: 6,
    fontSize: 11,
    fontWeight: 600,
    opacity: disabled ? 0.4 : 1,
  };
  return (
    <div
      style={{
        display: "flex",
        gap: 6,
        padding: 8,
        background: "rgba(0,0,0,0.7)",
        borderTop: "1px solid #262626",
        flexWrap: "wrap",
        justifyContent: "center",
      }}
    >
      <button type="button" style={btnStyle} disabled={disabled} onClick={() => { onShowKeyboard(); }}>
        ⌨ KEYBOARD
      </button>
      <button type="button" style={btnStyle} disabled={disabled} onClick={() => { sendCtrlAltDel(); }}>
        CTRL+ALT+DEL
      </button>
      <button type="button" style={btnStyle} disabled={disabled} onClick={() => { sendKey(0xff1b, "Escape"); }}>
        ESC
      </button>
      <button type="button" style={btnStyle} disabled={disabled} onClick={() => { sendKey(0xff09, "Tab"); }}>
        TAB
      </button>
      <button type="button" style={btnStyle} disabled={disabled} onClick={() => { sendKey(0xff51, "ArrowLeft"); }}>
        ←
      </button>
      <button type="button" style={btnStyle} disabled={disabled} onClick={() => { sendKey(0xff52, "ArrowUp"); }}>
        ↑
      </button>
      <button type="button" style={btnStyle} disabled={disabled} onClick={() => { sendKey(0xff54, "ArrowDown"); }}>
        ↓
      </button>
      <button type="button" style={btnStyle} disabled={disabled} onClick={() => { sendKey(0xff53, "ArrowRight"); }}>
        →
      </button>
    </div>
  );
}

/**
 * Translate a browser KeyboardEvent into an X11 keysym so it can be sent
 * via RFB.sendKey(). Covers ASCII printables, common control keys, and
 * the most-used special keys. Returns null for keys we don't know how to
 * map (in which case the caller should let the event through).
 */
function mapBrowserKeyToKeysym(
  e: React.KeyboardEvent<HTMLInputElement>,
): number | null {
  // Control / function keys via e.key
  switch (e.key) {
    case "Backspace": return 0xff08;
    case "Tab": return 0xff09;
    case "Enter": return 0xff0d;
    case "Escape": return 0xff1b;
    case "Delete": return 0xffff;
    case "Home": return 0xff50;
    case "End": return 0xff57;
    case "PageUp": return 0xff55;
    case "PageDown": return 0xff56;
    case "ArrowLeft": return 0xff51;
    case "ArrowUp": return 0xff52;
    case "ArrowRight": return 0xff53;
    case "ArrowDown": return 0xff54;
    case "Insert": return 0xff63;
    case "F1": return 0xffbe;
    case "F2": return 0xffbf;
    case "F3": return 0xffc0;
    case "F4": return 0xffc1;
    case "F5": return 0xffc2;
    case "F6": return 0xffc3;
    case "F7": return 0xffc4;
    case "F8": return 0xffc5;
    case "F9": return 0xffc6;
    case "F10": return 0xffc7;
    case "F11": return 0xffc8;
    case "F12": return 0xffc9;
    case "Shift": return 0xffe1;
    case "Control": return 0xffe3;
    case "Alt": return 0xffe9;
    case "Meta": return 0xffe7;
    case "CapsLock": return 0xffe5;
  }
  // Single printable characters: keysym is the unicode codepoint for
  // Latin-1 chars, otherwise 0x01000000 + codepoint.
  if (e.key.length === 1) {
    const code = e.key.codePointAt(0);
    if (code === undefined) return null;
    if (code <= 0x00ff) return code;
    return 0x01000000 + code;
  }
  return null;
}
