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
import { MAX_CONSOLE_AUTO_RETRIES, type ConsoleTab } from "../types/console";
import { useConsoleStore } from "@/stores/console-store";
import { useTaskLogStore } from "@/stores/task-log-store";
import { useVMAction } from "@/features/vms/api/vm-queries";
import { useGuestPowerSync } from "../hooks/useGuestPowerSync";
import { useIdleTabOnUnmount } from "../hooks/useIdleTabOnUnmount";
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
  const updateTabStatus = useConsoleStore((s) => s.updateTabStatus);
  const resolveAndReconnect = useConsoleStore((s) => s.resolveAndReconnect);
  const [rfb, setRfb] = useState<RFB | null>(null);
  const retryCountRef = useRef(0);

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

  const guestType = tab.type === "ct_vnc" ? "lxc" : undefined;

  useEffect(() => {
    if (!activated) return;

    // Restored tabs rehydrate as "idle"; flip to connecting now that we are
    // actually dialling. Any other status is left alone — notably the parked
    // "guest-stopped", which connect() below deliberately declines to reopen.
    if (
      useConsoleStore.getState().tabs.find((t) => t.id === tabId)?.status ===
      "idle"
    ) {
      updateTabStatus(tabId, "connecting");
    }

    // Scoped to THIS run of the effect, deliberately not a ref. When the
    // effect re-runs (manual reconnect, node change) the handlers wired up by
    // the previous run are still attached to the socket the cleanup is tearing
    // down, and their close events land a beat later. A component-lifetime ref
    // was set true by the cleanup and then immediately false again here, so
    // those stale handlers read their own teardown as a dropped connection and
    // scheduled a retry — which bumped reconnectKey, re-ran the effect, and
    // looped forever. A per-run flag stays true for the generation it belongs
    // to, so a superseded connection can never speak for the tab again.
    let closed = false;
    let ws: WebSocket | null = null;
    // Per-run for the same reason `closed` is, though these never actually
    // leaked: as refs they were correct only because the cleanup reset the
    // flag and cleared the timer, and the effect body reset the flag again on
    // entry. As locals the scoping is the language's job, not a reader's.
    let retryScheduled = false;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;

    const tabIsParked = () =>
      useConsoleStore.getState().tabs.find((t) => t.id === tabId)?.status ===
      "guest-stopped";

    // Single funnel for auto-reconnects. Both the RFB disconnect event and
    // ws.onclose fire for one drop — without the dedup flag they each
    // scheduled a timer (and the second overwrote retryTimer, leaking the
    // first), producing overlapping reconnect cycles.
    const scheduleRetry = () => {
      if (closed || retryScheduled) return;
      if (tabIsParked()) return; // guest is off — wait for power-on instead
      if (retryCountRef.current < MAX_CONSOLE_AUTO_RETRIES) {
        const delay = Math.min(1000 * 2 ** retryCountRef.current, 10000);
        retryCountRef.current++;
        retryScheduled = true;
        updateTabStatus(tabId, "reconnecting");
        retryTimer = setTimeout(() => {
          retryScheduled = false;
          // Safe if the tab was closed in the meantime — resolveAndReconnect
          // returns early for an id that is no longer in the store.
          void resolveAndReconnect(tabId);
        }, delay);
      } else {
        updateTabStatus(tabId, "disconnected");
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
        if (closed) return;
        console.error("[VNCViewer] failed to mint console token", err);
        updateTabStatus(tabId, "error");
        return;
      }

      if (closed) return;

      const wsUrl = buildVncWsUrl(clusterID, node, vmid, guestType);
      ws = new WebSocket(wsUrl, wsAuthProtocols(token));
      ws.binaryType = "arraybuffer";

      const localWs = ws; // narrow non-null binding for closures

      localWs.onmessage = (event: MessageEvent) => {
        // close() is asynchronous, so frames already queued on a superseded
        // socket still dispatch. Without this a stale generation could reach
        // the `connected` branch below and build a SECOND RFB into the same
        // container — overwriting rfbRef, handing VNCToolbar a doomed
        // instance, and leaking the live Proxmox console session when the
        // next cleanup disconnects the wrong one.
        if (closed) return;
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
              if (!containerRef.current) {
                console.error(
                  "[VNCViewer] containerRef is null at connected time",
                );
                return;
              }

              retryCountRef.current = 0;

              const options: Record<string, unknown> = {};
              if (msg.password) {
                options["credentials"] = { password: msg.password };
              }

              const rfbInstance = new RFB(
                containerRef.current,
                localWs,
                options,
              );
              rfbInstance.scaleViewport = true;
              rfbInstance.resizeSession = false;
              rfbInstance.focusOnClick = true;

              rfbInstance.addEventListener("connect", () => {
                updateTabStatus(tabId, "connected");
              });

              rfbInstance.addEventListener("disconnect", () => {
                // We closed this one ourselves. Whatever owns the tab now —
                // a newer connection, or nothing — must not have its rfbRef
                // cleared or a retry scheduled on its behalf.
                if (closed) return;

                rfbRef.current = null;
                setRfb(null);
                scheduleRetry();
              });

              rfbInstance.addEventListener("securityfailure", () => {
                updateTabStatus(tabId, "error");
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
                updateTabStatus(tabId, "guest-stopped");
              } else {
                updateTabStatus(tabId, "error");
              }
              return;
            }
          } catch {
            // Not JSON — ignore
          }
        }
      };

      localWs.onclose = () => {
        if (closed) return;
        if (!rfbRef.current) {
          // WS closed before RFB was established — auto-reconnect
          scheduleRetry();
        }
      };

      localWs.onerror = (event) => {
        console.error(
          "[VNCViewer] WS error event",
          "type:",
          event.type,
          "readyState:",
          localWs.readyState,
        );
        if (!closed && !tabIsParked()) {
          updateTabStatus(tabId, "error");
        }
      };
    };

    void connect();

    return () => {
      closed = true;
      clearTimeout(retryTimer);
      if (rfbRef.current) {
        rfbRef.current.disconnect();
        rfbRef.current = null;
        setRfb(null);
      } else {
        ws?.close();
      }
    };
    // Only re-run when the actual connection parameters change.
  }, [
    tabId,
    tab.type,
    clusterID,
    node,
    vmid,
    guestType,
    reconnectKey,
    activated,
    mintToken,
    updateTabStatus,
    resolveAndReconnect,
  ]);

  // Declared after the connect effect on purpose — see the hook.
  useIdleTabOnUnmount(tabId);

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
        <ConsoleStateOverlay tab={tab} onReconnect={handleManualReconnect} />
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
  onReconnect,
}: {
  tab: ConsoleTab;
  onReconnect: () => void;
}) {
  const actionMutation = useVMAction();
  const setPanelOpen = useTaskLogStore((s) => s.setPanelOpen);
  const setFocusedTask = useTaskLogStore((s) => s.setFocusedTask);

  const status = tab.status;
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
