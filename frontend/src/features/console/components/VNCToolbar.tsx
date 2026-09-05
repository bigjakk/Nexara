import { useCallback, useRef, useState } from "react";
import { toast } from "sonner";
import {
  Keyboard,
  Maximize2,
  Minimize2,
  ClipboardPaste,
  RectangleHorizontal,
  Move,
  Power,
  Camera,
  Info,
  ChevronDown,
  Disc,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type RFB from "@novnc/novnc";
import type { ConsoleTab } from "../types/console";
import { useConsoleStore } from "@/stores/console-store";
import { useVM, useVMAction, useVMConfig } from "@/features/vms/api/vm-queries";
import { lifecycleActions } from "@/features/vms/lib/vm-action-defs";
import type { VMAction } from "@/features/vms/types/vm";
import { useTaskLogStore } from "@/stores/task-log-store";
import { useMountISO } from "../api/console-queries";
import { ISOPickerDialog } from "./ISOPickerDialog";
import { typeTextIntoVnc } from "../lib/vnc-keys";

// X11 keysyms
const XK = {
  Control_L: 0xffe3,
  Alt_L: 0xffe9,
  F1: 0xffbe,
  Tab: 0xff09,
  Print: 0xff61,
  Sys_Req: 0xff15,
  Super_L: 0xffeb,
  F4: 0xffc1,
} as const;

function sendKeyCombo(rfb: RFB, keysyms: number[]) {
  for (const k of keysyms) rfb.sendKey(k, null, true);
  for (const k of [...keysyms].reverse()) rfb.sendKey(k, null, false);
}

/** How many distinct dropped characters to name in the paste warning. */
const MAX_LISTED_UNMAPPED = 8;

/**
 * Characters with no glyph of their own: control codes, format characters,
 * every kind of space (a non-breaking space pasted out of a web page is the
 * common case), and lone combining marks — printing one raw would show
 * nothing, or graft an accent onto the surrounding label text.
 */
const NO_GLYPH = /^[\p{C}\p{Z}\p{M}]$/u;

/** Renderable label for a dropped character. */
function describeChar(ch: string): string {
  if (!NO_GLYPH.test(ch)) return ch;
  const cp = ch.codePointAt(0) ?? 0;
  return `U+${cp.toString(16).toUpperCase().padStart(4, "0")}`;
}

interface VNCToolbarProps {
  rfb: RFB | null;
  tab: ConsoleTab;
}

export function VNCToolbar({ rfb, tab }: VNCToolbarProps) {
  const windowMode = useConsoleStore((s) => s.windowMode);
  const setWindowMode = useConsoleStore((s) => s.setWindowMode);
  const isMaximized = windowMode === "maximized";
  const [scaleMode, setScaleMode] = useState<"scale" | "resize">("scale");
  const [pasteOpen, setPasteOpen] = useState(false);
  const [confirmAction, setConfirmAction] = useState<{
    action: VMAction;
    label: string;
  } | null>(null);
  const [infoOpen, setInfoOpen] = useState(false);
  const [isoPickerOpen, setIsoPickerOpen] = useState(false);
  const pasteRef = useRef<HTMLTextAreaElement>(null);

  const hasResource = tab.resourceId !== undefined && tab.kind !== undefined;
  const vmKind = tab.kind ?? "vm";

  const { data: vmData } = useVM(tab.clusterID, tab.resourceId ?? "", vmKind);
  const vmStatus = vmData?.status.toLowerCase() ?? "";

  const isVM = tab.kind === "vm" || tab.type === "vm_vnc";
  const { data: vmConfig } = useVMConfig(
    tab.clusterID,
    isVM ? (tab.resourceId ?? "") : "",
  );
  // Find the CD-ROM device from VM config (could be ide0, ide2, scsi*, etc.)
  const cdromValue = (() => {
    if (!vmConfig) return "";
    for (const key of Object.keys(vmConfig)) {
      const val = vmConfig[key];
      if (typeof val === "string" && val.includes("media=cdrom")) return val;
    }
    return "";
  })();
  const hasISO = cdromValue.length > 0 && !cdromValue.startsWith("none,");
  const currentISO = hasISO ? (cdromValue.split(",")[0] ?? null) : null;
  const mountISO = useMountISO();

  const actionMutation = useVMAction();
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  const visibleActions = hasResource
    ? lifecycleActions.filter((a) => a.showWhen(vmStatus, vmKind))
    : [];

  function executeAction(action: VMAction, label: string) {
    if (!tab.resourceId || !tab.kind) return;
    const vmName = tab.label.split(": ")[1] ?? "";
    const description = `${label} ${vmName}`.trim();
    actionMutation.mutate(
      {
        clusterId: tab.clusterID,
        resourceId: tab.resourceId,
        kind: tab.kind,
        action,
      },
      {
        onSuccess: (data) => {
          setFocusedTask({
            clusterId: tab.clusterID,
            upid: data.upid,
            description,
          });
          setPanelOpen(true);
        },
      },
    );
    setConfirmAction(null);
  }

  function handlePowerAction(
    action: VMAction,
    label: string,
    needsConfirm: boolean,
  ) {
    if (needsConfirm) {
      setConfirmAction({ action, label });
    } else {
      executeAction(action, label);
    }
  }

  const handleToggleMaximize = useCallback(() => {
    setWindowMode(isMaximized ? "floating" : "maximized");
  }, [isMaximized, setWindowMode]);

  function handleSendPaste() {
    const text = pasteRef.current?.value;
    if (text && rfb) {
      // Partial success is the right outcome here: everything on a US layout
      // is typed, and the rest is reported rather than vanishing silently.
      const unmapped = typeTextIntoVnc(rfb, text);
      if (unmapped.length > 0) {
        const distinct = [...new Set(unmapped)];
        const listed = distinct
          .slice(0, MAX_LISTED_UNMAPPED)
          .map(describeChar)
          .join(" ");
        const more = distinct.length > MAX_LISTED_UNMAPPED ? " …" : "";
        toast.warning(
          `${String(unmapped.length)} character(s) could not be typed`,
          {
            description: `Not available on a US keyboard layout: ${listed}${more}`,
          },
        );
      }
    }
    setPasteOpen(false);
    rfb?.focus();
  }

  function handleScreenshot() {
    // noVNC renders into a canvas inside the container
    const canvas = document.querySelector<HTMLCanvasElement>(
      `[data-tab-id="${tab.id}"] canvas`,
    );
    if (!canvas) return;
    const link = document.createElement("a");
    const name = tab.label.replace(/[^a-zA-Z0-9-_]/g, "_");
    link.download = `console-${name}-${new Date().toISOString().slice(0, 19)}.png`;
    link.href = canvas.toDataURL("image/png");
    link.click();
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
      {/* Power controls */}
      {hasResource && visibleActions.length > 0 && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="sm"
              className="h-7 gap-1 px-2 text-xs"
            >
              <Power className="h-3.5 w-3.5" />
              Power
              <ChevronDown className="h-3 w-3" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            {visibleActions.map((config) => (
              <DropdownMenuItem
                key={config.action}
                onClick={() => {
                  handlePowerAction(
                    config.action,
                    config.label,
                    config.needsConfirm,
                  );
                }}
              >
                <span className="mr-2">{config.icon}</span>
                {config.label}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      )}

      {/* Media (ISO mount/eject) — VM only */}
      {isVM && tab.resourceId && (
        <>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="sm"
                className="h-7 gap-1 px-2 text-xs"
              >
                <Disc className="h-3.5 w-3.5" />
                Media
                <ChevronDown className="h-3 w-3" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              <DropdownMenuItem
                onClick={() => {
                  setIsoPickerOpen(true);
                }}
              >
                Mount ISO...
              </DropdownMenuItem>
              {hasISO && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    onClick={() => {
                      if (tab.resourceId) {
                        mountISO.mutate({
                          clusterId: tab.clusterID,
                          vmId: tab.resourceId,
                          volid: "none",
                        });
                      }
                    }}
                  >
                    Eject CD-ROM
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    disabled
                    className="text-xs text-muted-foreground"
                  >
                    Current: {currentISO}
                  </DropdownMenuItem>
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>

          <ISOPickerDialog
            open={isoPickerOpen}
            onOpenChange={setIsoPickerOpen}
            clusterId={tab.clusterID}
            node={tab.node}
            vmResourceId={tab.resourceId}
            currentISO={currentISO}
          />
        </>
      )}

      {/* Keyboard macros */}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs">
            <Keyboard className="h-3.5 w-3.5" />
            Keys
            <ChevronDown className="h-3 w-3" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="max-h-72 overflow-y-auto">
          <DropdownMenuItem
            onClick={() => {
              rfb?.sendCtrlAltDel();
            }}
          >
            Ctrl+Alt+Del
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          {Array.from({ length: 12 }, (_, i) => i + 1).map((n) => (
            <DropdownMenuItem
              key={`f${String(n)}`}
              onClick={() => {
                if (rfb)
                  sendKeyCombo(rfb, [XK.Control_L, XK.Alt_L, XK.F1 + n - 1]);
              }}
            >
              Ctrl+Alt+F{String(n)}
            </DropdownMenuItem>
          ))}
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              if (rfb) sendKeyCombo(rfb, [XK.Alt_L, XK.Tab]);
            }}
          >
            Alt+Tab
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              if (rfb) sendKeyCombo(rfb, [XK.Alt_L, XK.F4]);
            }}
          >
            Alt+F4
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              if (rfb) rfb.sendKey(XK.Print, null, true);
              rfb?.sendKey(XK.Print, null, false);
            }}
          >
            Print Screen
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              if (rfb) sendKeyCombo(rfb, [XK.Alt_L, XK.Sys_Req]);
            }}
          >
            SysRq
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {/* Paste */}
      <Button
        variant="ghost"
        size="sm"
        title="Paste text into console"
        className="h-7 px-2"
        onClick={() => {
          setPasteOpen(true);
        }}
      >
        <ClipboardPaste className="h-3.5 w-3.5" />
      </Button>

      {/* Screenshot */}
      <Button
        variant="ghost"
        size="sm"
        title="Screenshot"
        className="h-7 px-2"
        onClick={handleScreenshot}
      >
        <Camera className="h-3.5 w-3.5" />
      </Button>

      {/* Maximize / Restore (console window, not browser) */}
      <Button
        variant="ghost"
        size="sm"
        onClick={handleToggleMaximize}
        title={isMaximized ? "Restore" : "Maximize"}
        className="h-7 px-2"
      >
        {isMaximized ? (
          <Minimize2 className="h-3.5 w-3.5" />
        ) : (
          <Maximize2 className="h-3.5 w-3.5" />
        )}
      </Button>

      {/* Scale toggle */}
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

      {/* Connection info */}
      <Button
        variant="ghost"
        size="sm"
        title="Connection info"
        className="ml-auto h-7 px-2"
        onClick={() => {
          setInfoOpen(true);
        }}
      >
        <Info className="h-3.5 w-3.5" />
      </Button>

      {/* Paste dialog */}
      <Dialog open={pasteOpen} onOpenChange={setPasteOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Paste into Console</DialogTitle>
          </DialogHeader>
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              Paste your text below, then click Send to type it into the
              console.
            </p>
            <textarea
              ref={pasteRef}
              placeholder="Ctrl+V to paste here..."
              className="h-24 w-full rounded-md border bg-background px-3 py-2 text-sm focus:outline-hidden focus:ring-1 focus:ring-ring"
              autoFocus
            />
            <div className="flex justify-end gap-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setPasteOpen(false);
                }}
              >
                Cancel
              </Button>
              <Button size="sm" onClick={handleSendPaste}>
                Send
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      {/* Confirm dangerous action dialog */}
      <Dialog
        open={confirmAction !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmAction(null);
        }}
      >
        <DialogContent className="max-w-xs">
          <DialogHeader>
            <DialogTitle>Confirm {confirmAction?.label}</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-foreground">
            Are you sure you want to {confirmAction?.label.toLowerCase()} this{" "}
            {vmKind === "ct" ? "container" : "VM"}?
          </p>
          <div className="flex justify-end gap-2 pt-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setConfirmAction(null);
              }}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => {
                if (confirmAction)
                  executeAction(confirmAction.action, confirmAction.label);
              }}
            >
              {confirmAction?.label}
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      {/* Connection info dialog */}
      <Dialog open={infoOpen} onOpenChange={setInfoOpen}>
        <DialogContent className="max-w-xs">
          <DialogHeader>
            <DialogTitle>Connection Info</DialogTitle>
          </DialogHeader>
          <div className="space-y-1.5 text-sm">
            <div className="flex justify-between">
              <span className="text-muted-foreground">Node</span>
              <span className="font-medium">{tab.node}</span>
            </div>
            {tab.vmid !== undefined && (
              <div className="flex justify-between">
                <span className="text-muted-foreground">VMID</span>
                <span className="font-medium">{String(tab.vmid)}</span>
              </div>
            )}
            <div className="flex justify-between">
              <span className="text-muted-foreground">Type</span>
              <span className="font-medium">{tab.type}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">Status</span>
              <span className="font-medium capitalize">{tab.status}</span>
            </div>
            {vmData && (
              <div className="flex justify-between">
                <span className="text-muted-foreground">VM Status</span>
                <span className="font-medium capitalize">{vmData.status}</span>
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
