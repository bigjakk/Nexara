import { useEffect, useRef, useState } from "react";
import RFB from "@novnc/novnc/lib/rfb";
import type { ConsoleTab } from "../types/console";
import { useConsoleStore } from "@/stores/console-store";
import { VNCToolbar } from "./VNCToolbar";

interface VNCViewerProps {
  tab: ConsoleTab;
  visible: boolean;
  /**
   * Optional override for the access token used in the WS URL. When
   * provided, the component uses this token instead of reading
   * `localStorage.access_token`. Used by the /mobile-console route to pass
   * a short-lived scope-locked JWT minted via /api/v1/auth/console-token.
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

function buildVncWsUrl(
  clusterID: string,
  node: string,
  vmid?: number,
  guestType?: string,
  overrideToken?: string,
): string {
  // overrideToken is used by the /mobile-console route which receives a
  // short-lived scope-locked JWT in its URL params. Desktop usage falls
  // back to the access token in localStorage.
  const token = overrideToken ?? localStorage.getItem("access_token");
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const host = window.location.host;
  const params = new URLSearchParams({
    token: token ?? "",
    cluster_id: clusterID,
    node,
  });
  if (vmid !== undefined) {
    params.set("vmid", String(vmid));
  }
  if (guestType) {
    params.set("type", guestType);
  }
  return `${protocol}//${host}/ws/vnc?${params.toString()}`;
}

const MAX_AUTO_RETRIES = 3;

export function VNCViewer({ tab, visible, accessToken }: VNCViewerProps) {
  const { id: tabId, clusterID, node, vmid, reconnectKey } = tab;
  const containerRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<RFB | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const updateTabStatus = useConsoleStore((s) => s.updateTabStatus);
  const resolveAndReconnect = useConsoleStore((s) => s.resolveAndReconnect);
  const [rfb, setRfb] = useState<RFB | null>(null);
  const retryCountRef = useRef(0);
  const intentionalCloseRef = useRef(false);
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Store latest callbacks in refs so the effect doesn't depend on them.
  const updateTabStatusRef = useRef(updateTabStatus);
  updateTabStatusRef.current = updateTabStatus;
  const resolveAndReconnectRef = useRef(resolveAndReconnect);
  resolveAndReconnectRef.current = resolveAndReconnect;
  const tabIdRef = useRef(tabId);
  tabIdRef.current = tabId;

  const guestType = tab.type === "ct_vnc" ? "lxc" : undefined;

  useEffect(() => {
    intentionalCloseRef.current = false;
    const wsUrl = buildVncWsUrl(clusterID, node, vmid, guestType, accessToken);
    console.log("[VNCViewer] opening WS", wsUrl.replace(/token=[^&]+/, "token=***"));
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      console.log("[VNCViewer] WS open, readyState:", ws.readyState);
    };

    // Diagnostic: log readyState 1 second and 5 seconds after creation in
    // case onopen / onerror / onclose never fire (silent failure mode).
    setTimeout(() => {
      console.log(
        "[VNCViewer] WS state @ 1s",
        "readyState:", ws.readyState,
        "(0=connecting, 1=open, 2=closing, 3=closed)",
      );
    }, 1000);
    setTimeout(() => {
      console.log("[VNCViewer] WS state @ 5s readyState:", ws.readyState);
    }, 5000);

    ws.onmessage = (event: MessageEvent) => {
      if (typeof event.data === "string") {
        try {
          const msg = JSON.parse(event.data) as {
            type: string;
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

            const rfbInstance = new RFB(containerRef.current, ws, options);
            rfbInstance.scaleViewport = true;
            rfbInstance.resizeSession = false;
            rfbInstance.focusOnClick = true;

            rfbInstance.addEventListener("connect", () => {
              console.log("[VNCViewer] RFB connect event fired");
              updateTabStatusRef.current(tabIdRef.current, "connected");
            });

            rfbInstance.addEventListener("disconnect", () => {
              console.log("[VNCViewer] RFB disconnect event fired");
              if (intentionalCloseRef.current) {
                updateTabStatusRef.current(tabIdRef.current, "disconnected");
                rfbRef.current = null;
                setRfb(null);
                return;
              }

              rfbRef.current = null;
              setRfb(null);

              if (retryCountRef.current < MAX_AUTO_RETRIES) {
                const delay = Math.min(1000 * 2 ** retryCountRef.current, 10000);
                retryCountRef.current++;
                updateTabStatusRef.current(tabIdRef.current, "reconnecting");
                retryTimerRef.current = setTimeout(() => {
                  void resolveAndReconnectRef.current(tabIdRef.current);
                }, delay);
              } else {
                updateTabStatusRef.current(tabIdRef.current, "disconnected");
              }
            });

            rfbInstance.addEventListener("securityfailure", () => {
              updateTabStatusRef.current(tabIdRef.current, "error");
            });

            rfbRef.current = rfbInstance;
            setRfb(rfbInstance);
            return;
          }
          if (msg.type === "error") {
            updateTabStatusRef.current(tabIdRef.current, "error");
            return;
          }
        } catch {
          // Not JSON — ignore
        }
      }
    };

    ws.onclose = (event) => {
      console.log(
        "[VNCViewer] WS close",
        "code:", event.code,
        "reason:", event.reason || "(none)",
        "wasClean:", event.wasClean,
        "readyState:", ws.readyState,
      );
      if (intentionalCloseRef.current) return;
      if (!rfbRef.current) {
        // WS closed before RFB was established — auto-reconnect
        if (retryCountRef.current < MAX_AUTO_RETRIES) {
          const delay = Math.min(1000 * 2 ** retryCountRef.current, 10000);
          retryCountRef.current++;
          updateTabStatusRef.current(tabIdRef.current, "reconnecting");
          retryTimerRef.current = setTimeout(() => {
            void resolveAndReconnectRef.current(tabIdRef.current);
          }, delay);
        } else {
          updateTabStatusRef.current(tabIdRef.current, "disconnected");
        }
      }
    };

    ws.onerror = (event) => {
      console.error(
        "[VNCViewer] WS error event",
        "type:", event.type,
        "readyState:", ws.readyState,
      );
      if (!intentionalCloseRef.current) {
        updateTabStatusRef.current(tabIdRef.current, "error");
      }
    };

    return () => {
      intentionalCloseRef.current = true;
      clearTimeout(retryTimerRef.current);
      if (rfbRef.current) {
        rfbRef.current.disconnect();
        rfbRef.current = null;
        setRfb(null);
      } else {
        ws.close();
      }
      wsRef.current = null;
    };
    // Only re-run when the actual connection parameters change.
  }, [tabId, clusterID, node, vmid, guestType, reconnectKey, accessToken]);

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

  return (
    <div
      className="flex h-full flex-col"
      style={{ display: visible ? "flex" : "none" }}
    >
      {!isMobile && !isMinimized && <VNCToolbar rfb={rfb} tab={tab} />}
      <div
        ref={containerRef}
        className="flex-1 bg-black"
        data-tab-id={tab.id}
        onClick={isMobile ? focusMobileKeyboard : undefined}
      />
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
    color: "#22c55e",
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
