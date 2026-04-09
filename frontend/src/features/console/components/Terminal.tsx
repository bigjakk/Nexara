import { useEffect, useRef, useCallback } from "react";
import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import type { ConsoleTab } from "../types/console";
import { useConsoleStore } from "@/stores/console-store";

interface TerminalProps {
  tab: ConsoleTab;
  visible: boolean;
  /**
   * Optional override for the access token used in the WS URL. When
   * provided, the component uses this token instead of reading
   * `localStorage.access_token`. Used by the /mobile-console route to pass
   * a short-lived scope-locked JWT minted via /api/v1/auth/console-token.
   */
  accessToken?: string;
}

function buildConsoleWsUrl(
  clusterID: string,
  node: string,
  type: string,
  vmid?: number,
  overrideToken?: string,
): string {
  const token = overrideToken ?? localStorage.getItem("access_token");
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const host = window.location.host;
  const params = new URLSearchParams({
    token: token ?? "",
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

    // Connect WebSocket.
    intentionalCloseRef.current = false;
    const wsUrl = buildConsoleWsUrl(clusterID, node, type, vmid, accessToken);
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      // Wait for the "connected" message from server before updating status.
    };

    ws.onmessage = (event: MessageEvent) => {
      if (typeof event.data === "string") {
        // JSON control message.
        try {
          const msg = JSON.parse(event.data) as { type: string; message?: string };
          if (msg.type === "connected") {
            retryCountRef.current = 0;
            updateTabStatus(tabId, "connected");
            // Send initial resize.
            ws.send(
              JSON.stringify({
                type: "resize",
                cols: term.cols,
                rows: term.rows,
              }),
            );
            return;
          }
          if (msg.type === "error") {
            term.writeln(`\r\nError: ${msg.message ?? "unknown error"}`);
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
        term.writeln(`\r\n[Connection lost \u2014 reconnecting in ${String(delay / 1000)}s...]`);
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
    const dataDisposable = term.onData((data: string) => {
      if (ws.readyState === WebSocket.OPEN) {
        // Send as binary.
        const encoder = new TextEncoder();
        ws.send(encoder.encode(data));
      }
    });

    // Wire terminal resize to WebSocket.
    const resizeDisposable = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "resize", cols, rows }));
      }
    });

    // ResizeObserver for auto-fit.
    const observer = new ResizeObserver(() => {
      requestAnimationFrame(() => {
        try {
          fitAddon.fit();
        } catch {
          // Ignore
        }
      });
    });
    observer.observe(containerRef.current);

    return () => {
      intentionalCloseRef.current = true;
      clearTimeout(retryTimerRef.current);
      observer.disconnect();
      dataDisposable.dispose();
      resizeDisposable.dispose();
      ws.close();
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
