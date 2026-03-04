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
}

function buildConsoleWsUrl(tab: ConsoleTab): string {
  const token = localStorage.getItem("access_token");
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const host = window.location.host;
  const params = new URLSearchParams({
    token: token ?? "",
    cluster_id: tab.clusterID,
    node: tab.node,
    type: tab.type,
  });
  if (tab.vmid !== undefined) {
    params.set("vmid", String(tab.vmid));
  }
  return `${protocol}//${host}/ws/console?${params.toString()}`;
}

export function Terminal({ tab, visible }: TerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const updateTabStatus = useConsoleStore((s) => s.updateTabStatus);

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
    const wsUrl = buildConsoleWsUrl(tab);
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
            updateTabStatus(tab.id, "connected");
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
            updateTabStatus(tab.id, "error");
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
      updateTabStatus(tab.id, "disconnected");
      term.writeln("\r\n\r\n[Connection closed]");
    };

    ws.onerror = () => {
      updateTabStatus(tab.id, "error");
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
      observer.disconnect();
      dataDisposable.dispose();
      resizeDisposable.dispose();
      ws.close();
      term.dispose();
      termRef.current = null;
      wsRef.current = null;
      fitAddonRef.current = null;
    };
  }, [tab.id, tab.clusterID, tab.node, tab.type, tab.vmid, updateTabStatus]);

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
