import { useEffect, useRef, useState, useCallback } from "react";
import RFB from "@novnc/novnc/lib/rfb";
import { Keyboard, Maximize, Minimize, ClipboardPaste, Move, RectangleHorizontal, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";

interface InlineVNCViewerProps {
  clusterId: string;
  node: string;
  vmid: number;
  guestType?: "qemu" | "lxc" | undefined;
}

type VNCStatus = "connecting" | "connected" | "disconnected" | "error";

function buildVncWsUrl(clusterID: string, node: string, vmid: number, guestType?: string): string {
  const token = localStorage.getItem("access_token");
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const host = window.location.host;
  const params = new URLSearchParams({
    token: token ?? "",
    cluster_id: clusterID,
    node,
    vmid: String(vmid),
  });
  if (guestType) {
    params.set("type", guestType);
  }
  return `${protocol}//${host}/ws/vnc?${params.toString()}`;
}

export function InlineVNCViewer({ clusterId, node, vmid, guestType }: InlineVNCViewerProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<RFB | null>(null);
  const [rfb, setRfb] = useState<RFB | null>(null);
  const [status, setStatus] = useState<VNCStatus>("connecting");
  const [reconnectKey, setReconnectKey] = useState(0);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [scaleMode, setScaleMode] = useState<"scale" | "resize">("scale");

  useEffect(() => {
    const wsUrl = buildVncWsUrl(clusterId, node, vmid, guestType);
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    setStatus("connecting");

    ws.onmessage = (event: MessageEvent) => {
      if (typeof event.data === "string") {
        try {
          const msg = JSON.parse(event.data) as {
            type: string;
            password?: string;
          };
          if (msg.type === "connected") {
            if (!containerRef.current) return;
            const options: Record<string, unknown> = {};
            if (msg.password) {
              options["credentials"] = { password: msg.password };
            }
            const rfbInstance = new RFB(containerRef.current, ws, options);
            rfbInstance.scaleViewport = true;
            rfbInstance.resizeSession = false;
            rfbInstance.focusOnClick = true;

            rfbInstance.addEventListener("connect", () => { setStatus("connected"); });
            rfbInstance.addEventListener("disconnect", () => {
              setStatus("disconnected");
              rfbRef.current = null;
              setRfb(null);
            });
            rfbInstance.addEventListener("securityfailure", () => { setStatus("error"); });

            rfbRef.current = rfbInstance;
            setRfb(rfbInstance);
            return;
          }
          if (msg.type === "error") {
            setStatus("error");
            return;
          }
        } catch {
          // not JSON
        }
      }
    };

    ws.onclose = () => {
      if (!rfbRef.current) setStatus("disconnected");
    };
    ws.onerror = () => { setStatus("error"); };

    return () => {
      if (rfbRef.current) {
        rfbRef.current.disconnect();
        rfbRef.current = null;
        setRfb(null);
      } else {
        ws.close();
      }
    };
  }, [clusterId, node, vmid, guestType, reconnectKey]);

  const handleCtrlAltDel = useCallback(() => { rfb?.sendCtrlAltDel(); }, [rfb]);

  const handleFullscreen = useCallback(() => {
    const el = containerRef.current?.parentElement;
    if (!el) return;
    if (!document.fullscreenElement) {
      el.requestFullscreen().catch(() => {});
      setIsFullscreen(true);
    } else {
      document.exitFullscreen().catch(() => {});
      setIsFullscreen(false);
    }
  }, []);

  const handlePaste = useCallback(() => {
    if (!rfb) return;
    navigator.clipboard.readText().then((text) => { rfb.clipboardPasteFrom(text); }).catch(() => {});
  }, [rfb]);

  const handleScaleToggle = useCallback(() => {
    if (!rfb) return;
    if (scaleMode === "scale") {
      rfb.scaleViewport = false;
      rfb.resizeSession = true;
      setScaleMode("resize");
    } else {
      rfb.scaleViewport = true;
      rfb.resizeSession = false;
      setScaleMode("scale");
    }
  }, [rfb, scaleMode]);

  const handleReconnect = useCallback(() => { setReconnectKey((k) => k + 1); }, []);

  const statusLabel =
    status === "connecting" ? "Connecting..." :
    status === "connected" ? "Connected" :
    status === "error" ? "Connection error" :
    "Disconnected";

  return (
    <div className="flex flex-col rounded-lg border overflow-hidden">
      {/* Toolbar */}
      <div className="flex items-center gap-1 border-b bg-card px-2 py-1">
        <span className="mr-2 flex items-center gap-1.5 text-xs text-muted-foreground">
          <span className={`inline-block h-2 w-2 rounded-full ${
            status === "connected" ? "bg-green-500" :
            status === "connecting" ? "bg-yellow-500 animate-pulse" :
            "bg-red-500"
          }`} />
          {statusLabel}
        </span>
        <div className="flex-1" />
        {status === "connected" && (
          <>
            <Button variant="ghost" size="sm" onClick={handleCtrlAltDel} title="Send Ctrl+Alt+Del" className="h-7 gap-1 px-2 text-xs">
              <Keyboard className="h-3.5 w-3.5" />
              Ctrl+Alt+Del
            </Button>
            <Button variant="ghost" size="sm" onClick={handlePaste} title="Paste from clipboard" className="h-7 px-2">
              <ClipboardPaste className="h-3.5 w-3.5" />
            </Button>
            <Button variant="ghost" size="sm" onClick={handleScaleToggle} title={scaleMode === "scale" ? "Resize mode" : "Scale mode"} className="h-7 gap-1 px-2 text-xs">
              {scaleMode === "scale" ? <RectangleHorizontal className="h-3.5 w-3.5" /> : <Move className="h-3.5 w-3.5" />}
              {scaleMode === "scale" ? "Scale" : "Resize"}
            </Button>
            <Button variant="ghost" size="sm" onClick={handleFullscreen} title="Fullscreen" className="h-7 px-2">
              {isFullscreen ? <Minimize className="h-3.5 w-3.5" /> : <Maximize className="h-3.5 w-3.5" />}
            </Button>
          </>
        )}
        {(status === "disconnected" || status === "error") && (
          <Button variant="ghost" size="sm" onClick={handleReconnect} className="h-7 gap-1 px-2 text-xs">
            <RefreshCw className="h-3.5 w-3.5" />
            Reconnect
          </Button>
        )}
      </div>
      {/* VNC canvas */}
      <div ref={containerRef} className="bg-black" style={{ minHeight: "480px", height: "60vh" }} />
    </div>
  );
}
