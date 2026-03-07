import { useCallback, useRef, useState } from "react";
import {
  Keyboard,
  Maximize,
  Minimize,
  ClipboardPaste,
  RectangleHorizontal,
  Move,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type RFB from "@novnc/novnc/lib/rfb";
import { typeTextIntoVnc } from "./VNCViewer";

interface VNCToolbarProps {
  rfb: RFB | null;
}

export function VNCToolbar({ rfb }: VNCToolbarProps) {
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [scaleMode, setScaleMode] = useState<"scale" | "resize">("scale");
  const [pasteOpen, setPasteOpen] = useState(false);
  const pasteRef = useRef<HTMLTextAreaElement>(null);

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

  function handleSendPaste() {
    const text = pasteRef.current?.value;
    if (text && rfb) {
      typeTextIntoVnc(rfb, text);
    }
    setPasteOpen(false);
    rfb?.focus();
  }

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
        title="Paste text into console"
        className="h-7 px-2"
        onClick={() => { setPasteOpen(true); }}
      >
        <ClipboardPaste className="h-3.5 w-3.5" />
      </Button>
      <Dialog open={pasteOpen} onOpenChange={setPasteOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Paste into Console</DialogTitle>
          </DialogHeader>
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              Paste your text below, then click Send to type it into the console.
            </p>
            <textarea
              ref={pasteRef}
              placeholder="Ctrl+V to paste here..."
              className="h-24 w-full rounded-md border bg-background px-3 py-2 text-sm focus:outline-none focus:ring-1 focus:ring-ring"
              autoFocus
            />
            <div className="flex justify-end gap-2">
              <Button variant="ghost" size="sm" onClick={() => { setPasteOpen(false); }}>
                Cancel
              </Button>
              <Button size="sm" onClick={handleSendPaste}>
                Send
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
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
