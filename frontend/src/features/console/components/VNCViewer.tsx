import { useEffect, useRef, useState } from "react";
import RFB from "@novnc/novnc/lib/rfb";
import type { ConsoleTab } from "../types/console";
import { useConsoleStore } from "@/stores/console-store";
import { VNCToolbar } from "./VNCToolbar";

interface VNCViewerProps {
  tab: ConsoleTab;
  visible: boolean;
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
): string {
  const token = localStorage.getItem("access_token");
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

export function VNCViewer({ tab, visible }: VNCViewerProps) {
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
    const wsUrl = buildVncWsUrl(clusterID, node, vmid, guestType);
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

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
            if (!containerRef.current) return;

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
              updateTabStatusRef.current(tabIdRef.current, "connected");
            });

            rfbInstance.addEventListener("disconnect", () => {
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

    ws.onclose = () => {
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

    ws.onerror = () => {
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
  }, [tabId, clusterID, node, vmid, guestType, reconnectKey]);

  const isMinimized = useConsoleStore((s) => s.windowMode) === "minimized";

  return (
    <div
      className="flex h-full flex-col"
      style={{ display: visible ? "flex" : "none" }}
    >
      {!isMinimized && <VNCToolbar rfb={rfb} tab={tab} />}
      <div ref={containerRef} className="flex-1 bg-black" data-tab-id={tab.id} />
    </div>
  );
}
