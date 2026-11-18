import { useCallback, useRef, useState } from "react";
import { createPortal } from "react-dom";
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
import { cn } from "@/lib/utils";
import type { ConsoleTab } from "../types/console";
import { useConsoleSession } from "@/stores/console-store";
import { useVM, useVMAction, useVMConfig } from "@/features/vms/api/vm-queries";
import {
  actionVerb,
  lifecycleActions,
  useGuestActionAllowed,
} from "@/features/vms/lib/vm-action-defs";
import type { VMAction } from "@/features/vms/types/vm";
import { useTaskLogStore } from "@/stores/task-log-store";
import { useMountISO } from "../api/console-queries";
import { ISOPickerDialog } from "./ISOPickerDialog";
import { typeTextIntoVnc } from "../lib/vnc-keys";
import { guestState } from "@/lib/guest-state";
import { useTranslation } from "react-i18next";

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
  /**
   * The floating window's maximize/restore. A console in a browser tab of its
   * own has no window to size; its page offers fullscreen instead.
   */
  windowControls?: boolean;
  /**
   * Where the controls go when the console's page has a toolbar of its own:
   * they join it, so the display has one bar above it and not two. Null until
   * that toolbar has mounted. Omitted, the controls are a bar of their own.
   */
  slot?: HTMLElement | null | undefined;
}

/** A quiet control: an icon, and its word where the bar has the room. */
const TOOL =
  "h-7 shrink-0 gap-1.5 px-2 text-[12.5px] font-normal text-muted-foreground hover:text-foreground";

export function VNCToolbar({
  rfb,
  tab,
  windowControls = true,
  slot,
}: VNCToolbarProps) {
  const { t } = useTranslation("console");
  const windowMode = useConsoleSession((s) => s.windowMode);
  const setWindowMode = useConsoleSession((s) => s.setWindowMode);
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
  // Set by Send: the paste dialog then closes back to the console, to go on
  // typing into the guest, not to the Paste button that opened it.
  const pasteSent = useRef(false);

  const hasResource = tab.resourceId !== undefined && tab.kind !== undefined;
  const vmKind = tab.kind ?? "vm";

  const { data: vmData } = useVM(tab.clusterID, tab.resourceId ?? "", vmKind);
  // A halted VM's QEMU state, as the guest page's actions read it.
  const vmStatus = vmData ? guestState(vmData.status, vmData.run_state) : "";

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

  const allowed = useGuestActionAllowed();
  const visibleActions = hasResource
    ? lifecycleActions.filter(
        (a) => a.showWhen(vmStatus, vmKind) && allowed(a.action, vmKind),
      )
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
          t("vncToolbar.lengthCharacterSCouldNot", {
            length: String(unmapped.length),
          }),
          {
            description: t("vncToolbar.notAvailableOnAUS", { listed, more }),
          },
        );
      }
    }
    pasteSent.current = true;
    setPasteOpen(false);
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

  // The words beside the icons show once the bar they sit in is wide enough:
  // sooner in a bar of their own than in one shared with the console's name.
  const word = slot === undefined ? "hidden @2xl:inline" : "hidden @5xl:inline";

  const controls = (
    <>
      {/* Power controls */}
      {hasResource && visibleActions.length > 0 && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="sm"
              aria-label={t("vncToolbar.power")}
              title={t("vncToolbar.power")}
              className={TOOL}
            >
              <Power className="h-3.5 w-3.5" />
              <span className={word}>{t("vncToolbar.power")}</span>
              <ChevronDown className="h-3 w-3 opacity-60" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            {visibleActions.map((config) => (
              <DropdownMenuItem
                key={config.action}
                onClick={() => {
                  handlePowerAction(
                    config.action,
                    actionVerb(config),
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

      {/* Media (ISO mount/eject) — VM only, and execute:vm */}
      {isVM && tab.resourceId && allowed("media", "vm") && (
        <>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="sm"
                aria-label={t("vncToolbar.media")}
                title={t("vncToolbar.media")}
                className={TOOL}
              >
                <Disc className="h-3.5 w-3.5" />
                <span className={word}>{t("vncToolbar.media")}</span>
                <ChevronDown className="h-3 w-3 opacity-60" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              <DropdownMenuItem
                onClick={() => {
                  setIsoPickerOpen(true);
                }}
              >
                {t("vncToolbar.mountISO")}
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
                    {t("vncToolbar.ejectCDROM")}
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    disabled
                    className="text-xs text-muted-foreground"
                  >
                    {t("vncToolbar.currentCurrentISO", { currentISO })}
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
          <Button
            variant="ghost"
            size="sm"
            aria-label={t("vncToolbar.keys")}
            title={t("vncToolbar.sendKeys")}
            className={TOOL}
          >
            <Keyboard className="h-3.5 w-3.5" />
            <span className={word}>{t("vncToolbar.keys")}</span>
            <ChevronDown className="h-3 w-3 opacity-60" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="max-h-72 overflow-y-auto">
          <DropdownMenuItem
            onClick={() => {
              rfb?.sendCtrlAltDel();
            }}
          >
            {t("vncToolbar.ctrlAltDel")}
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
              {t("vncToolbar.ctrlAltFN", { n: String(n) })}
            </DropdownMenuItem>
          ))}
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              if (rfb) sendKeyCombo(rfb, [XK.Alt_L, XK.Tab]);
            }}
          >
            {t("vncToolbar.altTab")}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              if (rfb) sendKeyCombo(rfb, [XK.Alt_L, XK.F4]);
            }}
          >
            {t("vncToolbar.altF4")}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              if (rfb) rfb.sendKey(XK.Print, null, true);
              rfb?.sendKey(XK.Print, null, false);
            }}
          >
            {t("vncToolbar.printScreen")}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              if (rfb) sendKeyCombo(rfb, [XK.Alt_L, XK.Sys_Req]);
            }}
          >
            {t("vncToolbar.sysRq")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      {/* Paste */}
      <Button
        variant="ghost"
        size="sm"
        aria-label={t("vncToolbar.pasteTextIntoConsole")}
        title={t("vncToolbar.pasteTextIntoConsole")}
        className={TOOL}
        onClick={() => {
          setPasteOpen(true);
        }}
      >
        <ClipboardPaste className="h-3.5 w-3.5" />
        <span className={word}>{t("vncToolbar.paste")}</span>
      </Button>

      {/* Screenshot */}
      <Button
        variant="ghost"
        size="sm"
        aria-label={t("vncToolbar.screenshot")}
        title={t("vncToolbar.screenshot")}
        className={TOOL}
        onClick={handleScreenshot}
      >
        <Camera className="h-3.5 w-3.5" />
        <span className={word}>{t("vncToolbar.screenshot")}</span>
      </Button>

      {/* Scale toggle */}
      <Button
        variant="ghost"
        size="sm"
        onClick={handleScaleToggle}
        aria-label={
          scaleMode === "scale" ? t("vncToolbar.scale") : t("vncToolbar.resize")
        }
        title={
          scaleMode === "scale"
            ? t("vncToolbar.switchToResizeMode")
            : t("vncToolbar.switchToScaleMode")
        }
        className={TOOL}
      >
        {scaleMode === "scale" ? (
          <RectangleHorizontal className="h-3.5 w-3.5" />
        ) : (
          <Move className="h-3.5 w-3.5" />
        )}
        <span className={word}>
          {scaleMode === "scale"
            ? t("vncToolbar.scale")
            : t("vncToolbar.resize")}
        </span>
      </Button>

      {/* Connection info */}
      <Button
        variant="ghost"
        size="sm"
        aria-label={t("vncToolbar.connectionInfo")}
        title={t("vncToolbar.connectionInfo")}
        className={TOOL}
        onClick={() => {
          setInfoOpen(true);
        }}
      >
        <Info className="h-3.5 w-3.5" />
        <span className={word}>{t("vncToolbar.info")}</span>
      </Button>

      {/* Maximize / Restore (console window, not browser) */}
      {windowControls && (
        <Button
          variant="ghost"
          size="sm"
          onClick={handleToggleMaximize}
          title={
            isMaximized ? t("vncToolbar.restore") : t("vncToolbar.maximize")
          }
          className={cn(TOOL, "ml-auto")}
        >
          {isMaximized ? (
            <Minimize2 className="h-3.5 w-3.5" />
          ) : (
            <Maximize2 className="h-3.5 w-3.5" />
          )}
        </Button>
      )}
    </>
  );

  const dialogs = (
    <>
      {/* Paste dialog */}
      <Dialog open={pasteOpen} onOpenChange={setPasteOpen}>
        <DialogContent
          className="max-w-sm"
          onCloseAutoFocus={(event) => {
            // As the dialog closes, not in Send: while it is open its focus
            // trap takes focus straight back from the console.
            const sent = pasteSent.current;
            pasteSent.current = false;
            if (!sent || rfb === null) return;
            event.preventDefault();
            rfb.focus();
          }}
        >
          <DialogHeader>
            <DialogTitle>{t("vncToolbar.pasteIntoConsole")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              {t("vncToolbar.pasteYourTextBelowThen")}
            </p>
            <textarea
              ref={pasteRef}
              placeholder={t("vncToolbar.ctrlVToPasteHere")}
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
                {t("vncToolbar.cancel")}
              </Button>
              <Button size="sm" onClick={handleSendPaste}>
                {t("vncToolbar.send")}
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
            <DialogTitle>
              {t("vncToolbar.confirmLabel", { label: confirmAction?.label })}
            </DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-foreground">
            {vmKind === "ct"
              ? t("vncToolbar.confirmActionContainer", {
                  label: confirmAction?.label.toLowerCase(),
                })
              : t("vncToolbar.confirmActionVm", {
                  label: confirmAction?.label.toLowerCase(),
                })}
          </p>
          <div className="flex justify-end gap-2 pt-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setConfirmAction(null);
              }}
            >
              {t("vncToolbar.cancel")}
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
            <DialogTitle>{t("vncToolbar.connectionInfo2")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-1.5 text-sm">
            <div className="flex justify-between">
              <span className="text-muted-foreground">
                {t("vncToolbar.node")}
              </span>
              <span className="font-medium">{tab.node}</span>
            </div>
            {tab.vmid !== undefined && (
              <div className="flex justify-between">
                <span className="text-muted-foreground">
                  {t("vncToolbar.vmid")}
                </span>
                <span className="font-medium">{String(tab.vmid)}</span>
              </div>
            )}
            <div className="flex justify-between">
              <span className="text-muted-foreground">
                {t("vncToolbar.type")}
              </span>
              <span className="font-medium">{tab.type}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-muted-foreground">
                {t("vncToolbar.status")}
              </span>
              <span className="font-medium capitalize">{tab.status}</span>
            </div>
            {vmData && (
              <div className="flex justify-between">
                <span className="text-muted-foreground">
                  {t("vncToolbar.vmStatus")}
                </span>
                <span className="font-medium capitalize">{vmData.status}</span>
              </div>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </>
  );

  // Joined to the page's toolbar. The dialogs stay here: they portal
  // themselves, and are not the toolbar's to lay out.
  if (slot !== undefined) {
    return (
      <>
        {slot !== null && createPortal(controls, slot)}
        {dialogs}
      </>
    );
  }
  return (
    <div className="@container border-b bg-card">
      <div className="flex items-center gap-0.5 overflow-x-auto px-2 py-1">
        {controls}
      </div>
      {dialogs}
    </div>
  );
}
