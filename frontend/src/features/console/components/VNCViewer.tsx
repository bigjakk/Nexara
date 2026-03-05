import { useEffect, useRef, useState } from "react";
import RFB from "@novnc/novnc/lib/rfb";
import type { ConsoleTab } from "../types/console";
import { useConsoleStore } from "@/stores/console-store";
import { VNCToolbar } from "./VNCToolbar";

interface VNCViewerProps {
  tab: ConsoleTab;
  visible: boolean;
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

export function VNCViewer({ tab, visible }: VNCViewerProps) {
  const { id: tabId, clusterID, node, vmid, reconnectKey } = tab;
  const containerRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<RFB | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const updateTabStatus = useConsoleStore((s) => s.updateTabStatus);
  const [rfb, setRfb] = useState<RFB | null>(null);

  // Store latest callbacks in refs so the effect doesn't depend on them.
  const updateTabStatusRef = useRef(updateTabStatus);
  updateTabStatusRef.current = updateTabStatus;
  const tabIdRef = useRef(tabId);
  tabIdRef.current = tabId;

  const guestType = tab.type === "ct_vnc" ? "lxc" : undefined;

  useEffect(() => {
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
              updateTabStatusRef.current(tabIdRef.current, "disconnected");
              rfbRef.current = null;
              setRfb(null);
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
      if (!rfbRef.current) {
        updateTabStatusRef.current(tabIdRef.current, "disconnected");
      }
    };

    ws.onerror = () => {
      updateTabStatusRef.current(tabIdRef.current, "error");
    };

    return () => {
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabId, clusterID, node, vmid, guestType, reconnectKey]);

  return (
    <div
      className="flex h-full flex-col"
      style={{ display: visible ? "flex" : "none" }}
    >
      <VNCToolbar rfb={rfb} />
      <div ref={containerRef} className="flex-1 bg-black" />
    </div>
  );
}
