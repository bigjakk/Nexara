import { useEffect, useRef, useState, useCallback } from "react";
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
  return `${protocol}//${host}/ws/vnc?${params.toString()}`;
}

export function VNCViewer({ tab, visible }: VNCViewerProps) {
  const { id: tabId, clusterID, node, vmid } = tab;
  const containerRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<RFB | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const updateTabStatus = useConsoleStore((s) => s.updateTabStatus);
  const [rfb, setRfb] = useState<RFB | null>(null);

  const initRFB = useCallback(
    (ws: WebSocket) => {
      if (!containerRef.current) return;

      const rfbInstance = new RFB(containerRef.current, ws);
      rfbInstance.scaleViewport = true;
      rfbInstance.resizeSession = false;
      rfbInstance.focusOnClick = true;

      rfbInstance.addEventListener("connect", () => {
        updateTabStatus(tabId, "connected");
      });

      rfbInstance.addEventListener("disconnect", () => {
        updateTabStatus(tabId, "disconnected");
        rfbRef.current = null;
        setRfb(null);
      });

      rfbInstance.addEventListener("securityfailure", () => {
        updateTabStatus(tabId, "error");
      });

      rfbRef.current = rfbInstance;
      setRfb(rfbInstance);
    },
    [tabId, updateTabStatus],
  );

  useEffect(() => {
    const wsUrl = buildVncWsUrl(clusterID, node, vmid);
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onmessage = (event: MessageEvent) => {
      if (typeof event.data === "string") {
        try {
          const msg = JSON.parse(event.data) as {
            type: string;
            message?: string;
          };
          if (msg.type === "connected") {
            // Backend proxy is connected to Proxmox — now initialize noVNC RFB.
            initRFB(ws);
            return;
          }
          if (msg.type === "error") {
            updateTabStatus(tabId, "error");
            return;
          }
        } catch {
          // Not JSON — ignore
        }
      }
    };

    ws.onclose = () => {
      if (!rfbRef.current) {
        updateTabStatus(tabId, "disconnected");
      }
    };

    ws.onerror = () => {
      updateTabStatus(tabId, "error");
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
  }, [tabId, clusterID, node, vmid, updateTabStatus, initRFB]);

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
