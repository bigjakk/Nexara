import { useEffect, useRef, useCallback } from "react";
import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import type { ConsoleTab } from "../types/console";
import { useConsoleStore } from "@/stores/console-store";
import {
  mintConsoleToken,
  wsAuthProtocols,
  type ConsoleScopeType,
} from "../api/console-queries";

interface TerminalProps {
  tab: ConsoleTab;
  visible: boolean;
  /**
   * Optional pre-minted scoped console token. When provided, the component
   * skips the inline mint and uses this token directly (mobile passes a
   * token minted upstream by its native shell). When omitted, the desktop
   * flow mints via POST /api/v1/auth/console-token before opening the WS.
   *
   * Either way the token rides in `Sec-WebSocket-Protocol` (per remediation
   * 2.7) — never in the URL — so it's not exposed in proxy access logs or
   * Referer headers. The /ws/console endpoint rejects regular access tokens
   * (per-cluster RBAC enforcement, security fix #1).
   */
  accessToken?: string;
}

function buildConsoleWsUrl(
  clusterID: string,
  node: string,
  type: string,
  vmid: number | undefined,
): string {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const host = window.location.host;
  // Token is delivered via Sec-WebSocket-Protocol (subprotocol); the URL
  // only carries scope-validation params for the backend's exact-match
  // check against the JWT's ConsoleScope.
  const params = new URLSearchParams({
    cluster_id: clusterID,
    node,
    type,
  });
  if (vmid !== undefined) {
    params.set("vmid", String(vmid));
  }
  return `${protocol}//${host}/ws/console?${params.toString()}`;
}

const MAX_AUTO_RETRIES = 3;

export function Terminal({ tab, visible, accessToken }: TerminalProps) {
  const { id: tabId, clusterID, node, type, vmid, reconnectKey } = tab;
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const updateTabStatus = useConsoleStore((s) => s.updateTabStatus);
  const resolveAndReconnect = useConsoleStore((s) => s.resolveAndReconnect);
  const retryCountRef = useRef(0);
  const intentionalCloseRef = useRef(false);
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const handleResize = useCallback(() => {
    if (fitAddonRef.current && termRef.current && visible) {
      try {
        fitAddonRef.current.fit();
      } catch {
        // Ignore fit errors when container is hidden
      }
    }
  }, [visible]);

  useEffect(() => {
    if (!containerRef.current) return;

    const term = new XTerminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace",
      theme: {
        background: "#1a1b26",
        foreground: "#a9b1d6",
        cursor: "#c0caf5",
        selectionBackground: "#33467c",
      },
      scrollback: 5000,
      convertEol: true,
    });

    const fitAddon = new FitAddon();
    const webLinksAddon = new WebLinksAddon();

    term.loadAddon(fitAddon);
    term.loadAddon(webLinksAddon);
    term.open(containerRef.current);

    termRef.current = term;
    fitAddonRef.current = fitAddon;

    // Fit after opening.
    requestAnimationFrame(() => {
      try {
        fitAddon.fit();
      } catch {
        // Ignore
      }
    });

    // Resources captured by the cleanup function. Populated asynchronously
    // by connect() once the scoped token has been minted; cleanup tolerates
    // them still being null if unmount races the mint.
    intentionalCloseRef.current = false;
    let ws: WebSocket | null = null;
    let dataDisposable: { dispose: () => void } | null = null;
    let resizeDisposable: { dispose: () => void } | null = null;
    let observer: ResizeObserver | null = null;

    const connect = async () => {
      // Acquire the WS upgrade token. Desktop callers omit accessToken and
      // mint a short-lived scoped JWT; mobile passes its pre-minted token
      // through the prop.
      let token: string;
      try {
        if (accessToken) {
          token = accessToken;
        } else {
          const minted = await mintConsoleToken({
            clusterId: clusterID,
            node,
            type: type as ConsoleScopeType,
            ...(vmid !== undefined ? { vmid } : {}),
          });
          token = minted.token;
        }
      } catch (err) {
        if (intentionalCloseRef.current) return;
        const msg = err instanceof Error ? err.message : "unknown error";
        term.writeln(`\r\nFailed to authorize console session: ${msg}`);
        updateTabStatus(tabId, "error");
        return;
      }

      // Component may have unmounted while we awaited the mint.
      if (intentionalCloseRef.current) return;

      const wsUrl = buildConsoleWsUrl(clusterID, node, type, vmid);
      ws = new WebSocket(wsUrl, wsAuthProtocols(token));
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      ws.onopen = () => {
        // Wait for the "connected" message from server before updating status.
      };

      ws.onmessage = (event: MessageEvent) => {
        if (typeof event.data === "string") {
          // JSON control message.
          try {
            const parsed = JSON.parse(event.data) as {
              type: string;
              message?: string;
            };
            if (parsed.type === "connected") {
              retryCountRef.current = 0;
              updateTabStatus(tabId, "connected");
              // Send initial resize.
              ws?.send(
                JSON.stringify({
                  type: "resize",
                  cols: term.cols,
                  rows: term.rows,
                }),
              );
              return;
            }
            if (parsed.type === "error") {
              term.writeln(`\r\nError: ${parsed.message ?? "unknown error"}`);
              updateTabStatus(tabId, "error");
              return;
            }
          } catch {
            // Not JSON, write as text.
            term.write(event.data);
          }
        } else if (event.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(event.data));
        }
      };

      ws.onclose = () => {
        if (intentionalCloseRef.current) return;

        if (retryCountRef.current < MAX_AUTO_RETRIES) {
          const delay = Math.min(1000 * 2 ** retryCountRef.current, 10000);
          retryCountRef.current++;
          updateTabStatus(tabId, "reconnecting");
          term.writeln(
            `\r\n[Connection lost \u2014 reconnecting in ${String(delay / 1000)}s...]`,
          );
          retryTimerRef.current = setTimeout(() => {
            void resolveAndReconnect(tabId);
          }, delay);
        } else {
          updateTabStatus(tabId, "disconnected");
          term.writeln("\r\n\r\n[Connection closed]");
        }
      };

      ws.onerror = () => {
        if (!intentionalCloseRef.current) {
          updateTabStatus(tabId, "error");
        }
      };

      // Wire terminal input to WebSocket.
      dataDisposable = term.onData((data: string) => {
        if (ws && ws.readyState === WebSocket.OPEN) {
          const encoder = new TextEncoder();
          ws.send(encoder.encode(data));
        }
      });

      // Wire terminal resize to WebSocket.
      resizeDisposable = term.onResize(({ cols, rows }) => {
        if (ws && ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: "resize", cols, rows }));
        }
      });

      // ResizeObserver for auto-fit.
      observer = new ResizeObserver(() => {
        requestAnimationFrame(() => {
          try {
            fitAddon.fit();
          } catch {
            // Ignore
          }
        });
      });
      if (containerRef.current) {
        observer.observe(containerRef.current);
      }
    };

    void connect();

    return () => {
      intentionalCloseRef.current = true;
      clearTimeout(retryTimerRef.current);
      observer?.disconnect();
      dataDisposable?.dispose();
      resizeDisposable?.dispose();
      ws?.close();
      term.dispose();
      termRef.current = null;
      wsRef.current = null;
      fitAddonRef.current = null;
    };
  }, [tabId, clusterID, node, type, vmid, reconnectKey, accessToken, updateTabStatus, resolveAndReconnect]);

  // Re-fit when visibility changes.
  useEffect(() => {
    if (visible) {
      handleResize();
    }
  }, [visible, handleResize]);

  return (
    <div
      ref={containerRef}
      className="h-full w-full"
      style={{ display: visible ? "block" : "none" }}
    />
  );
}
