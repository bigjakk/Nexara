import { useCallback, useState } from "react";
import {
  Keyboard,
  Maximize,
  Minimize,
  ClipboardPaste,
  RectangleHorizontal,
  Move,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import type RFB from "@novnc/novnc/lib/rfb";

interface VNCToolbarProps {
  rfb: RFB | null;
}

export function VNCToolbar({ rfb }: VNCToolbarProps) {
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [scaleMode, setScaleMode] = useState<"scale" | "resize">("scale");

  const handleCtrlAltDel = useCallback(() => {
    rfb?.sendCtrlAltDel();
  }, [rfb]);

  const handleFullscreen = useCallback(() => {
    if (!document.fullscreenElement) {
      document.documentElement.requestFullscreen().catch(() => {
        // Fullscreen not supported or denied
      });
      setIsFullscreen(true);
    } else {
      document.exitFullscreen().catch(() => {
        // Already not in fullscreen
      });
      setIsFullscreen(false);
    }
  }, []);

  const handleClipboardPaste = useCallback(() => {
    if (!rfb) return;
    navigator.clipboard
      .readText()
      .then((text) => {
        rfb.clipboardPasteFrom(text);
      })
      .catch(() => {
        // Clipboard read denied or not available
      });
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

  return (
    <div className="flex items-center gap-1 border-b border-border bg-background px-2 py-1">
      <Button
        variant="ghost"
        size="sm"
        onClick={handleCtrlAltDel}
        title="Send Ctrl+Alt+Del"
        className="h-7 gap-1 px-2 text-xs"
      >
        <Keyboard className="h-3.5 w-3.5" />
        Ctrl+Alt+Del
      </Button>
      <Button
        variant="ghost"
        size="sm"
        onClick={handleFullscreen}
        title={isFullscreen ? "Exit Fullscreen" : "Fullscreen"}
        className="h-7 px-2"
      >
        {isFullscreen ? (
          <Minimize className="h-3.5 w-3.5" />
        ) : (
          <Maximize className="h-3.5 w-3.5" />
        )}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        onClick={handleClipboardPaste}
        title="Paste from clipboard"
        className="h-7 px-2"
      >
        <ClipboardPaste className="h-3.5 w-3.5" />
      </Button>
      <Button
        variant="ghost"
        size="sm"
        onClick={handleScaleToggle}
        title={
          scaleMode === "scale"
            ? "Switch to resize mode"
            : "Switch to scale mode"
        }
        className="h-7 gap-1 px-2 text-xs"
      >
        {scaleMode === "scale" ? (
          <RectangleHorizontal className="h-3.5 w-3.5" />
        ) : (
          <Move className="h-3.5 w-3.5" />
        )}
        {scaleMode === "scale" ? "Scale" : "Resize"}
      </Button>
    </div>
  );
}
