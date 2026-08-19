import { useEffect, useRef, useState } from "react";
import RFB from "@novnc/novnc";
import {
  AlertCircle,
  Loader2,
  Play,
  PowerOff,
  RotateCcw,
  Unplug,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  MAX_CONSOLE_AUTO_RETRIES,
  type ConsoleStatus,
  type ConsoleTab,
} from "../types/console";
import { useConsoleStore } from "@/stores/console-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { useVMAction } from "@/features/vms/api/vm-queries";
import { useGuestPowerSync } from "../hooks/useGuestPowerSync";
import { VNCToolbar } from "./VNCToolbar";
import {
  buildVncWsUrl,
  createConsoleTokenMinter,
  wsAuthProtocols,
} from "../api/console-queries";

interface VNCViewerProps {
  tab: ConsoleTab;
  visible: boolean;
}

export function VNCViewer({ tab, visible }: VNCViewerProps) {
  const { id: tabId, clusterID, node, vmid, reconnectKey } = tab;
  const containerRef = useRef<HTMLDivElement>(null);
  const rfbRef = useRef<RFB | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const updateTabStatus = useConsoleStore((s) => s.updateTabStatus);
  const resolveAndReconnect = useConsoleStore((s) => s.resolveAndReconnect);
  const [rfb, setRfb] = useState<RFB | null>(null);
  const retryCountRef = useRef(0);
  const retryScheduledRef = useRef(false);
  const intentionalCloseRef = useRef(false);
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Reuses a still-valid scoped token across this tab's reconnect cycle
  // rather than minting — and auditing — one per attempt. Created once via
  // the lazy useState initializer so the cache survives effect re-runs.
  const [mintToken] = useState(createConsoleTokenMinter);

  // A tab dials Proxmox the first time it becomes the active tab, and stays
  // connected after that — switching away must never tear down a live
  // session. Tabs restored from localStorage therefore sit idle until you
  // actually look at them, instead of every persisted session reconnecting
  // at once on login. An explicit reconnect (reconnectKey > 0) also counts
  // as activation, so the tab-bar reconnect button works on a background tab.
  const [activated, setActivated] = useState(visible || reconnectKey > 0);
  useEffect(() => {
    if (visible || reconnectKey > 0) {
      setActivated(true);
    }
  }, [visible, reconnectKey]);

  // Park dead tabs while the guest is off; auto-resume when it powers on.
  useGuestPowerSync(tab);

  // Store latest callbacks in refs so the effect doesn't depend on them.
  const resolveAndReconnectRef = useRef(resolveAndReconnect);
  resolveAndReconnectRef.current = resolveAndReconnect;
  const tabIdRef = useRef(tabId);
  tabIdRef.current = tabId;

  const applyStatus = (status: ConsoleStatus) => {
    updateTabStatus(tabId, status);
  };
  const applyStatusRef = useRef(applyStatus);
  applyStatusRef.current = applyStatus;

  const guestType = tab.type === "ct_vnc" ? "lxc" : undefined;

  useEffect(() => {
    if (!activated) return;

    // Restored tabs rehydrate as "idle"; flip to connecting now that we are
    // actually dialling. Any other status is left alone — notably the parked
    // "guest-stopped", which connect() below deliberately declines to reopen.
    if (
      useConsoleStore.getState().tabs.find((t) => t.id === tabIdRef.current)
        ?.status === "idle"
    ) {
      applyStatusRef.current("connecting");
    }

    intentionalCloseRef.current = false;
    retryScheduledRef.current = false;
    let ws: WebSocket | null = null;
    let stateLog1Timer: ReturnType<typeof setTimeout> | null = null;
    let stateLog2Timer: ReturnType<typeof setTimeout> | null = null;

    const tabIsParked = () =>
      useConsoleStore.getState().tabs.find((t) => t.id === tabIdRef.current)
        ?.status === "guest-stopped";

    // Single funnel for auto-reconnects. Both the RFB disconnect event and
    // ws.onclose fire for one drop — without the dedup flag they each
    // scheduled a timer (and the second overwrote retryTimerRef, leaking the
    // first), producing overlapping reconnect cycles.
    const scheduleRetry = () => {
      if (intentionalCloseRef.current || retryScheduledRef.current) return;
      if (tabIsParked()) return; // guest is off — wait for power-on instead
      if (retryCountRef.current < MAX_CONSOLE_AUTO_RETRIES) {
        const delay = Math.min(1000 * 2 ** retryCountRef.current, 10000);
        retryCountRef.current++;
        retryScheduledRef.current = true;
        applyStatusRef.current("reconnecting");
        retryTimerRef.current = setTimeout(() => {
          retryScheduledRef.current = false;
          // Safe if the tab was closed in the meantime — resolveAndReconnect
          // returns early for an id that is no longer in the store.
          void resolveAndReconnectRef.current(tabIdRef.current);
        }, delay);
      } else {
        applyStatusRef.current("disconnected");
      }
    };

    const connect = async () => {
      // Remounted (e.g. minimize/restore) while parked on a stopped guest —
      // don't reopen a connection that's known to fail; useGuestPowerSync
      // resumes the tab when the guest powers on.
      if (tabIsParked()) return;

      // Mint the short-lived scoped WS upgrade token. It rides in
      // `Sec-WebSocket-Protocol` (per remediation 2.7) — never in the URL — so
      // it is not exposed in proxy access logs or Referer headers. The /ws/vnc
      // endpoint rejects regular access tokens (per-cluster RBAC enforcement,
      // security fix #1).
      //
      // The VNC scope type matches the tab type directly here — Terminal uses
      // node_shell/vm_serial/ct_attach, VNCViewer uses vm_vnc/ct_vnc. The VNC
      // subset is what tab.type can hold for this component.
      let token: string;
      try {
        token = await mintToken({
          clusterId: clusterID,
          node,
          type: tab.type,
          ...(vmid !== undefined ? { vmid } : {}),
        });
      } catch (err) {
        if (intentionalCloseRef.current) return;
        console.error("[VNCViewer] failed to mint console token", err);
        applyStatusRef.current("error");
        return;
      }

      if (intentionalCloseRef.current) return;

      const wsUrl = buildVncWsUrl(clusterID, node, vmid, guestType);
      // The wsUrl is now token-free (token rides in subprotocol). Log it.
      console.log(
        "[VNCViewer] opening WS",
        wsUrl,
        JSON.stringify({ clusterID, node, vmid, guestType }),
      );
      ws = new WebSocket(wsUrl, wsAuthProtocols(token));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      const localWs = ws; // narrow non-null binding for closures

      localWs.onopen = () => {
        console.log("[VNCViewer] WS open, readyState:", localWs.readyState);
      };

      // Diagnostic: log readyState 1 second and 5 seconds after creation in
      // case onopen / onerror / onclose never fire (silent failure mode).
      stateLog1Timer = setTimeout(() => {
        console.log(
          "[VNCViewer] WS state @ 1s",
          "readyState:", localWs.readyState,
          "(0=connecting, 1=open, 2=closing, 3=closed)",
        );
      }, 1000);
      stateLog2Timer = setTimeout(() => {
        console.log("[VNCViewer] WS state @ 5s readyState:", localWs.readyState);
      }, 5000);

      localWs.onmessage = (event: MessageEvent) => {
        if (typeof event.data === "string") {
          try {
            const msg = JSON.parse(event.data) as {
              type: string;
              code?: string;
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

              const rfbInstance = new RFB(containerRef.current, localWs, options);
              rfbInstance.scaleViewport = true;
              rfbInstance.resizeSession = false;
              rfbInstance.focusOnClick = true;

              rfbInstance.addEventListener("connect", () => {
                console.log("[VNCViewer] RFB connect event fired");
                applyStatusRef.current("connected");
              });

              rfbInstance.addEventListener("disconnect", () => {
                console.log("[VNCViewer] RFB disconnect event fired");
                if (intentionalCloseRef.current) {
                  applyStatusRef.current("disconnected");
                  rfbRef.current = null;
                  setRfb(null);
                  return;
                }

                rfbRef.current = null;
                setRfb(null);
                scheduleRetry();
              });

              rfbInstance.addEventListener("securityfailure", () => {
                applyStatusRef.current("error");
              });

              rfbRef.current = rfbInstance;
              setRfb(rfbInstance);
              return;
            }
            if (msg.type === "error") {
              if (msg.code === "guest_not_running") {
                // Backend confirmed the guest is powered off — park with a
                // fresh retry budget for when it comes back.
                retryCountRef.current = 0;
                applyStatusRef.current("guest-stopped");
              } else {
                applyStatusRef.current("error");
              }
              return;
            }
          } catch {
            // Not JSON — ignore
          }
        }
      };

      localWs.onclose = (event) => {
        console.log(
          "[VNCViewer] WS close",
          "code:", event.code,
          "reason:", event.reason || "(none)",
          "wasClean:", event.wasClean,
          "readyState:", localWs.readyState,
        );
        if (intentionalCloseRef.current) return;
        if (!rfbRef.current) {
          // WS closed before RFB was established — auto-reconnect
          scheduleRetry();
        }
      };

      localWs.onerror = (event) => {
        console.error(
          "[VNCViewer] WS error event",
          "type:", event.type,
          "readyState:", localWs.readyState,
        );
        if (!intentionalCloseRef.current && !tabIsParked()) {
          applyStatusRef.current("error");
        }
      };
    };

    void connect();

    return () => {
      intentionalCloseRef.current = true;
      clearTimeout(retryTimerRef.current);
      retryScheduledRef.current = false;
      if (stateLog1Timer) clearTimeout(stateLog1Timer);
      if (stateLog2Timer) clearTimeout(stateLog2Timer);
      if (rfbRef.current) {
        rfbRef.current.disconnect();
        rfbRef.current = null;
        setRfb(null);
      } else {
        ws?.close();
      }
      wsRef.current = null;
    };
    // Only re-run when the actual connection parameters change.
  }, [tabId, tab.type, clusterID, node, vmid, guestType, reconnectKey, activated, mintToken]);

  const isMinimized = useConsoleStore((s) => s.windowMode) === "minimized";

  function handleManualReconnect() {
    useConsoleStore.getState().reconnectTab(tabId);
  }

  return (
    <div
      className="flex h-full flex-col"
      style={{ display: visible ? "flex" : "none" }}
    >
      {!isMinimized && <VNCToolbar rfb={rfb} tab={tab} />}
      <div className="relative flex-1 overflow-hidden">
        <div
          ref={containerRef}
          className="h-full w-full bg-black"
          data-tab-id={tab.id}
        />
        <ConsoleStateOverlay
          tab={tab}
          status={tab.status}
          onReconnect={handleManualReconnect}
        />
      </div>
    </div>
  );
}

/**
 * Centered overlay communicating the console's connection state. Replaces
 * the old behavior of a bare black canvas for every non-connected state.
 * The "guest-stopped" state is the parked powered-off console: it offers a
 * Start button and notes that the console resumes on power-on (driven by
 * useGuestPowerSync in the parent).
 */
function ConsoleStateOverlay({
  tab,
  status,
  onReconnect,
}: {
  tab: ConsoleTab;
  status: ConsoleStatus;
  onReconnect: () => void;
}) {
  const actionMutation = useVMAction();
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  if (status === "connected") return null;

  const isCt = tab.kind === "ct" || tab.type === "ct_vnc";
  const kindLabel = isCt ? "Container" : "VM";
  const hasResource = tab.resourceId !== undefined && tab.kind !== undefined;

  function handleStart() {
    if (!tab.resourceId || !tab.kind) return;
    const guestName = tab.label.split(": ")[1] ?? "";
    actionMutation.mutate(
      {
        clusterId: tab.clusterID,
        resourceId: tab.resourceId,
        kind: tab.kind,
        action: "start",
      },
      {
        onSuccess: (data) => {
          setFocusedTask({
            clusterId: tab.clusterID,
            upid: data.upid,
            description: `Start ${guestName}`.trim(),
          });
          setPanelOpen(true);
        },
      },
    );
  }

  // "idle" is the pre-dial state of a restored background tab. It only
  // surfaces here for the frame between becoming visible and the connect
  // effect firing, so show the same spinner rather than a stale "no
  // connection" panel the user would be tempted to click.
  if (
    status === "idle" ||
    status === "connecting" ||
    status === "reconnecting"
  ) {
    return (
      <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-2 bg-black/60 text-muted-foreground">
        <Loader2 className="h-6 w-6 animate-spin" />
        <p className="text-xs">
          {status === "reconnecting" ? "Reconnecting…" : "Connecting…"}
        </p>
      </div>
    );
  }

  if (status === "guest-stopped") {
    return (
      <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-black/80 px-4 text-center">
        <PowerOff className="h-8 w-8 text-muted-foreground" />
        <div>
          <p className="text-sm font-medium text-foreground">
            {kindLabel} is powered off
          </p>
          {hasResource && (
            <p className="mt-1 text-xs text-muted-foreground">
              The console will connect automatically when it powers on.
            </p>
          )}
        </div>
        <div className="flex flex-wrap items-center justify-center gap-2">
          {hasResource && (
            <Button
              size="sm"
              className="h-7 gap-1.5 px-3 text-xs"
              disabled={actionMutation.isPending}
              onClick={handleStart}
            >
              {actionMutation.isPending ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Play className="h-3.5 w-3.5" />
              )}
              {actionMutation.isPending ? "Starting…" : `Start ${kindLabel}`}
            </Button>
          )}
          <Button
            variant="outline"
            size="sm"
            className="h-7 gap-1.5 px-3 text-xs"
            onClick={onReconnect}
          >
            <RotateCcw className="h-3.5 w-3.5" />
            Connect anyway
          </Button>
        </div>
      </div>
    );
  }

  // disconnected / error
  const isError = status === "error";
  return (
    <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 bg-black/80 px-4 text-center">
      {isError ? (
        <AlertCircle className="h-8 w-8 text-destructive" />
      ) : (
        <Unplug className="h-8 w-8 text-muted-foreground" />
      )}
      <div>
        <p className="text-sm font-medium text-foreground">
          {isError ? "Console connection failed" : "Console disconnected"}
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          {isError
            ? "The console session could not be established."
            : "Automatic reconnect attempts were exhausted."}
        </p>
      </div>
      <Button
        variant="outline"
        size="sm"
        className="h-7 gap-1.5 px-3 text-xs"
        onClick={onReconnect}
      >
        <RotateCcw className="h-3.5 w-3.5" />
        {isError ? "Retry" : "Reconnect"}
      </Button>
    </div>
  );
}
