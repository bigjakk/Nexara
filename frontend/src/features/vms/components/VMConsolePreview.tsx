import { useEffect, useRef, useState } from "react";
import RFB from "@novnc/novnc/lib/rfb";
import { Maximize2 } from "lucide-react";

interface VMConsolePreviewProps {
  clusterId: string;
  node: string;
  vmid: number;
  onOpen: () => void;
}

type PreviewStatus = "connecting" | "connected" | "paused" | "error";

function buildVncWsUrl(clusterID: string, node: string, vmid: number): string {
  const token = localStorage.getItem("access_token");
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const host = window.location.host;
  const params = new URLSearchParams({
    token: token ?? "",
    cluster_id: clusterID,
    node,
    vmid: String(vmid),
  });
  return `${protocol}//${host}/ws/vnc?${params.toString()}`;
}

export function VMConsolePreview({ clusterId, node, vmid, onOpen }: VMConsolePreviewProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<RFB | null>(null);
  const [status, setStatus] = useState<PreviewStatus>("connecting");
  const [visible, setVisible] = useState(
    () => typeof document === "undefined" || document.visibilityState === "visible",
  );

  // Pause the stream when the tab is hidden so we don't burn a Proxmox VNC slot
  // for a thumbnail no one is looking at. Resume on visible.
  useEffect(() => {
    const onVisibility = () => {
      setVisible(document.visibilityState === "visible");
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, []);

  useEffect(() => {
    if (!visible) {
      if (rfbRef.current) {
        rfbRef.current.disconnect();
        rfbRef.current = null;
      }
      setStatus("paused");
      return;
    }
    if (!node) return;

    setStatus("connecting");
    const ws = new WebSocket(buildVncWsUrl(clusterId, node, vmid));
    ws.binaryType = "arraybuffer";

    ws.onmessage = (event: MessageEvent) => {
      if (typeof event.data === "string") {
        try {
          const msg = JSON.parse(event.data) as { type: string; password?: string };
          if (msg.type === "connected") {
            if (!containerRef.current) return;
            const options: Record<string, unknown> = {};
            if (msg.password) {
              options["credentials"] = { password: msg.password };
            }
            const rfbInstance = new RFB(containerRef.current, ws, options);
            rfbInstance.scaleViewport = true;
            rfbInstance.resizeSession = false;
            rfbInstance.viewOnly = true;
            rfbInstance.focusOnClick = false;

            rfbInstance.addEventListener("connect", () => { setStatus("connected"); });
            rfbInstance.addEventListener("disconnect", () => {
              setStatus("error");
              rfbRef.current = null;
            });
            rfbInstance.addEventListener("securityfailure", () => { setStatus("error"); });

            rfbRef.current = rfbInstance;
            return;
          }
          if (msg.type === "error") {
            setStatus("error");
          }
        } catch {
          // not JSON
        }
      }
    };

    ws.onclose = () => {
      if (!rfbRef.current) setStatus("error");
    };
    ws.onerror = () => { setStatus("error"); };

    return () => {
      if (rfbRef.current) {
        rfbRef.current.disconnect();
        rfbRef.current = null;
      } else {
        ws.close();
      }
    };
  }, [clusterId, node, vmid, visible]);

  return (
    <button
      type="button"
      onClick={onOpen}
      className="group relative block h-[140px] w-[240px] shrink-0 overflow-hidden rounded-lg border bg-black ring-offset-background transition hover:ring-2 hover:ring-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
      title="Open VNC console"
      aria-label="Open VNC console"
    >
      <div ref={containerRef} className="pointer-events-none h-full w-full" />
      {status !== "connected" && (
        <div className="absolute inset-0 flex items-center justify-center bg-black/60 text-xs text-muted-foreground">
          {status === "connecting" && "Connecting…"}
          {status === "paused" && "Paused"}
          {status === "error" && "Console unavailable"}
        </div>
      )}
      <div className="pointer-events-none absolute bottom-2 right-2 flex items-center gap-1 rounded bg-background/90 px-2 py-1 text-xs font-medium opacity-0 transition-opacity group-hover:opacity-100">
        <Maximize2 className="h-3 w-3" />
        Open console
      </div>
    </button>
  );
}
