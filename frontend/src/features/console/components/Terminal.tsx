import { useEffect, useRef, useState } from "react";
import { Terminal as XTerminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { MAX_CONSOLE_AUTO_RETRIES, type ConsoleTab } from "../types/console";
import { useConsoleSession, useConsoleStoreApi } from "@/stores/console-store";
import { useGuestPowerSync } from "../hooks/useGuestPowerSync";
import { useIdleTabOnUnmount } from "../hooks/useIdleTabOnUnmount";
import {
  createConsoleTokenMinter,
  wsAuthProtocols,
} from "../api/console-queries";

interface TerminalProps {
  tab: ConsoleTab;
  visible: boolean;
}

/**
 * Opens a link the guest printed in a tab of its own, with no opener and no
 * referrer. Opened from this page, as xterm's and the addon's own handlers do,
 * it would share this tab's browsing context group — and start with a copy of
 * its sessionStorage, where a console tab keeps who it runs for — and carry
 * Nexara's address as its referrer.
 */
function openGuestLink(uri: string) {
  window.open(uri, "_blank", "noopener,noreferrer");
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

export function Terminal({ tab, visible }: TerminalProps) {
  const { id: tabId, clusterID, node, type, vmid, reconnectKey } = tab;
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const store = useConsoleStoreApi();
  const updateTabStatus = useConsoleSession((s) => s.updateTabStatus);
  const resolveAndReconnect = useConsoleSession((s) => s.resolveAndReconnect);
  const retryCountRef = useRef(0);

  // Reuses a still-valid scoped token across this tab's reconnect cycle
  // rather than minting — and auditing — one per attempt. Created once via
  // the lazy useState initializer so the cache survives effect re-runs.
  const [mintToken] = useState(createConsoleTokenMinter);

  // A tab dials Proxmox the first time it becomes the active tab, and stays
  // connected after that — switching away must never kill a live shell.
  // Tabs restored from localStorage therefore sit idle until you actually
  // look at them, instead of every persisted session reconnecting at once on
  // login. An explicit reconnect (reconnectKey > 0) also counts as
  // activation, so the tab-bar reconnect button works on a background tab.
  const [activated, setActivated] = useState(visible || reconnectKey > 0);
  useEffect(() => {
    if (visible || reconnectKey > 0) {
      setActivated(true);
    }
  }, [visible, reconnectKey]);

  // Park dead tabs while the guest is off; auto-resume when it powers on.
  useGuestPowerSync(tab);

  // Minimizing must not resize the guest's pty — see the ResizeObserver below.
  const isMinimized = useConsoleSession((s) => s.windowMode) === "minimized";

  useEffect(() => {
    if (!activated) return;
    if (!containerRef.current) return;

    // Restored tabs rehydrate as "idle"; flip to connecting now that we are
    // actually dialling. Any other status is left alone — notably the parked
    // "guest-stopped", which connect() below deliberately declines to reopen.
    if (store.getState().tabs.find((t) => t.id === tabId)?.status === "idle") {
      updateTabStatus(tabId, "connecting");
    }

    // Scoped to THIS run. As a ref it was correct only because the cleanup
    // below cleared it; as a local the scoping is the language's job.
    let retryTimer: ReturnType<typeof setTimeout> | undefined;

    const term = new XTerminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily:
        "'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace",
      theme: {
        background: "#1a1b26",
        foreground: "#a9b1d6",
        cursor: "#c0caf5",
        selectionBackground: "#33467c",
      },
      scrollback: 5000,
      convertEol: true,
      // OSC 8 links, whose text need not be where they go: asked first, as
      // xterm's own handler does, but naming the host the parser finds, so a
      // user part before it cannot pass for it and a lookalike shows as
      // punycode, and then the whole URL as the parser reads it (anything
      // hidden in the rest percent-encoded). xterm passes only http(s) ones,
      // and so does this, should that change.
      linkHandler: {
        activate: (_event, uri) => {
          let url: URL;
          try {
            url = new URL(uri);
          } catch {
            return;
          }
          if (url.protocol !== "http:" && url.protocol !== "https:") return;
          if (
            confirm(
              `Do you want to navigate to ${url.host}?\n\n${url.href}\n\nWARNING: This link could potentially be dangerous`,
            )
          ) {
            openGuestLink(url.href);
          }
        },
      },
    });

    const fitAddon = new FitAddon();
    const webLinksAddon = new WebLinksAddon((_event, uri) => {
      openGuestLink(uri);
    });

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
    //
    // `closed` is scoped to THIS run of the effect, deliberately not a ref.
    // When the effect re-runs (manual reconnect, node change) the previous
    // run's socket is still closing and its onclose lands a beat later. A
    // component-lifetime ref was set true by the cleanup and then immediately
    // false again here, so that stale handler read its own teardown as a
    // dropped connection and scheduled a retry — which bumped reconnectKey,
    // re-ran the effect, and looped forever.
    let closed = false;
    let ws: WebSocket | null = null;
    let dataDisposable: { dispose: () => void } | null = null;
    let resizeDisposable: { dispose: () => void } | null = null;
    let observer: ResizeObserver | null = null;

    const tabIsParked = () =>
      store.getState().tabs.find((t) => t.id === tabId)?.status ===
      "guest-stopped";

    const connect = async () => {
      // Remounted while parked on a stopped guest — don't reopen a
      // connection that's known to fail; useGuestPowerSync resumes the tab
      // when the guest powers on.
      if (tabIsParked()) {
        term.writeln(
          "[Guest is powered off — the console will connect when it powers on]",
        );
        return;
      }

      // Mint the short-lived scoped WS upgrade token. It rides in
      // `Sec-WebSocket-Protocol` (per remediation 2.7) — never in the URL — so
      // it is not exposed in proxy access logs or Referer headers. The
      // /ws/console endpoint rejects regular access tokens (per-cluster RBAC
      // enforcement, security fix #1).
      let token: string;
      try {
        token = await mintToken({
          clusterId: clusterID,
          node,
          type: type,
          ...(vmid !== undefined ? { vmid } : {}),
        });
      } catch (err) {
        if (closed) return;
        const msg = err instanceof Error ? err.message : "unknown error";
        term.writeln(`\r\nFailed to authorize console session: ${msg}`);
        updateTabStatus(tabId, "error");
        return;
      }

      // Component may have unmounted while we awaited the mint.
      if (closed) return;

      const wsUrl = buildConsoleWsUrl(clusterID, node, type, vmid);
      ws = new WebSocket(wsUrl, wsAuthProtocols(token));
      ws.binaryType = "arraybuffer";

      ws.onmessage = (event: MessageEvent) => {
        // close() is asynchronous, so frames already queued on a superseded
        // socket still dispatch. Without this the stale generation would
        // stamp its status onto the tab the live one is dialling, and write
        // into an XTerminal the cleanup has already disposed.
        if (closed) return;
        if (typeof event.data === "string") {
          // JSON control message.
          try {
            const parsed = JSON.parse(event.data) as {
              type: string;
              code?: string;
              message?: string;
            };
            if (parsed.type === "connected") {
              // The socket opening is not the connection: the backend still has
              // to reach Proxmox, so the status waits for this message rather
              // than for ws.onopen.
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
              if (parsed.code === "guest_not_running") {
                // Backend confirmed the guest is powered off \u2014 park with a
                // fresh retry budget for when it comes back.
                retryCountRef.current = 0;
                term.writeln(
                  "\r\n[Guest is powered off \u2014 the console will connect when it powers on]",
                );
                updateTabStatus(tabId, "guest-stopped");
              } else {
                term.writeln(`\r\nError: ${parsed.message ?? "unknown error"}`);
                updateTabStatus(tabId, "error");
              }
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
        if (closed) return;
        if (tabIsParked()) return; // guest is off \u2014 wait for power-on instead

        if (retryCountRef.current < MAX_CONSOLE_AUTO_RETRIES) {
          const delay = Math.min(1000 * 2 ** retryCountRef.current, 10000);
          retryCountRef.current++;
          updateTabStatus(tabId, "reconnecting");
          term.writeln(
            `\r\n[Connection lost \u2014 reconnecting in ${String(delay / 1000)}s...]`,
          );
          retryTimer = setTimeout(() => {
            void resolveAndReconnect(tabId);
          }, delay);
        } else {
          updateTabStatus(tabId, "disconnected");
          term.writeln("\r\n\r\n[Connection closed]");
        }
      };

      ws.onerror = () => {
        if (!closed && !tabIsParked()) {
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
          // fit() reflows the REMOTE pty, not just the local view: it fires
          // term.onResize, which sends a resize frame and SIGWINCHes whatever
          // is running. The minimized PiP is 320x200 — about 38x11 — so
          // fitting to it would redraw a live vim/htop/tmux into a fraction
          // of its columns and leave it mangled on restore. The PiP is a
          // thumbnail, not a viewport: hold the session at its real size and
          // re-fit when the window comes back (the effect below). This never
          // mattered before, because minimizing used to destroy the terminal
          // and dial a brand-new shell.
          if (store.getState().windowMode === "minimized") return;
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
      closed = true;
      clearTimeout(retryTimer);
      observer?.disconnect();
      dataDisposable?.dispose();
      resizeDisposable?.dispose();
      ws?.close();
      term.dispose();
      termRef.current = null;
      fitAddonRef.current = null;
    };
  }, [
    tabId,
    clusterID,
    node,
    type,
    vmid,
    reconnectKey,
    activated,
    mintToken,
    store,
    updateTabStatus,
    resolveAndReconnect,
  ]);

  // Declared after the connect effect on purpose — see the hook.
  useIdleTabOnUnmount(tabId);

  // Re-fit when the tab becomes visible, and again when the window is
  // restored from the PiP.
  useEffect(() => {
    if (!visible || isMinimized) return;
    if (!fitAddonRef.current || !termRef.current) return;
    try {
      fitAddonRef.current.fit();
    } catch {
      // Ignore fit errors when the container is hidden.
    }
  }, [visible, isMinimized]);

  return (
    <div
      ref={containerRef}
      className="h-full w-full"
      style={{ display: visible ? "block" : "none" }}
    />
  );
}
