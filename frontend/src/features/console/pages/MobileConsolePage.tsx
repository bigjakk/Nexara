/**
 * MobileConsolePage — full-screen, no-chrome console viewer mounted at the
 * public route `/mobile-console`. Designed to be loaded inside a React
 * Native WebView from the Nexara mobile app.
 *
 * URL contract (all required):
 *   /mobile-console
 *     ?cluster_id=<UUID>
 *     &node=<NODE_NAME>
 *     &type=<vm_vnc|ct_vnc|vm_serial|ct_attach|node_shell>
 *     &token=<scope-locked JWT minted via /api/v1/auth/console-token>
 *     &vmid=<INT>            // required for everything except node_shell
 *     &label=<URL-encoded display name>   // optional, used in title
 *
 * The route is intentionally outside the ProtectedRoute group — it does
 * not depend on localStorage / Zustand auth state, only on the JWT in the
 * URL. The WebSocket upgrade on the backend re-validates that the JWT's
 * console_scope matches the cluster_id/node/vmid/type query params, so a
 * mobile WebView with no other auth context can connect safely.
 */

import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { VNCViewer } from "../components/VNCViewer";
import { Terminal } from "../components/Terminal";
import type { ConsoleTab, ConsoleType } from "../types/console";

const VALID_TYPES = new Set<ConsoleType>([
  "node_shell",
  "vm_serial",
  "ct_attach",
  "vm_vnc",
  "ct_vnc",
]);

export default function MobileConsolePage() {
  const [params] = useSearchParams();

  const clusterId = params.get("cluster_id") ?? "";
  const node = params.get("node") ?? "";
  const typeRaw = params.get("type") ?? "";
  const token = params.get("token") ?? "";
  const vmidRaw = params.get("vmid");
  const label = params.get("label") ?? "Console";

  console.log(
    "[mobile-console] mounted",
    JSON.stringify({
      clusterId,
      node,
      typeRaw,
      vmid: vmidRaw,
      hasToken: token.length > 0,
      tokenLen: token.length,
    }),
  );

  const type = (VALID_TYPES.has(typeRaw as ConsoleType) ? typeRaw : "") as
    | ConsoleType
    | "";

  const vmid = useMemo(() => {
    if (!vmidRaw) return undefined;
    const n = Number.parseInt(vmidRaw, 10);
    return Number.isFinite(n) ? n : undefined;
  }, [vmidRaw]);

  // Build a synthetic ConsoleTab the existing components expect. This
  // doesn't touch the floating-console store — the components only read
  // from `tab` props for connection params.
  const tab = useMemo<ConsoleTab | null>(() => {
    if (!clusterId || !node || !type || !token) return null;
    if (type !== "node_shell" && vmid === undefined) return null;
    const synthetic: ConsoleTab = {
      id: `mobile-${clusterId}-${node}-${vmid !== undefined ? String(vmid) : "node"}`,
      clusterID: clusterId,
      node,
      type,
      label,
      status: "connecting",
      reconnectKey: 0,
    };
    if (vmid !== undefined) {
      synthetic.vmid = vmid;
    }
    return synthetic;
  }, [clusterId, node, type, vmid, token, label]);

  if (!tab) {
    console.error(
      "[mobile-console] missing required params, refusing to render",
    );
    return (
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          height: "100vh",
          backgroundColor: "#0a0a0a",
          color: "#fafafa",
          fontFamily: "system-ui, sans-serif",
          padding: "1rem",
          textAlign: "center",
        }}
      >
        <div>
          <p style={{ fontSize: 14, opacity: 0.8 }}>
            Missing or invalid console parameters.
          </p>
          <p style={{ fontSize: 12, opacity: 0.5, marginTop: 8 }}>
            Required: cluster_id, node, type, token{" "}
            {type !== "node_shell" ? "+ vmid" : ""}
          </p>
        </div>
      </div>
    );
  }

  const isVnc = tab.type === "vm_vnc" || tab.type === "ct_vnc";

  console.log(
    "[mobile-console] rendering",
    isVnc ? "VNCViewer" : "Terminal",
    "tabId:",
    tab.id,
  );

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        backgroundColor: "#000",
        display: "flex",
        flexDirection: "column",
      }}
    >
      {isVnc ? (
        <VNCViewer tab={tab} visible={true} accessToken={token} />
      ) : (
        <Terminal tab={tab} visible={true} accessToken={token} />
      )}
    </div>
  );
}
