import type {
  ConsoleResourceKind,
  ConsoleTarget,
  ConsoleType,
} from "../types/console";

/**
 * The page that runs one console in a browser tab of its own
 * (ConsoleSessionPage). Its query names the console; nothing else is kept, so
 * a reload, a bookmark or a link reopens the same one.
 */
export const CONSOLE_SESSION_PATH = "/console/session";

/** The label's first word, per console type. */
const LABEL_PREFIX: Record<ConsoleType, string> = {
  node_shell: "Shell",
  vm_serial: "Serial",
  vm_vnc: "VNC",
  ct_attach: "CT",
  ct_vnc: "CT",
};

// Not Object.hasOwn, which is newer than the browsers the build targets.
function isConsoleType(value: string | null): value is ConsoleType {
  return (
    value !== null && Object.prototype.hasOwnProperty.call(LABEL_PREFIX, value)
  );
}

/** The kind of guest a guest console type opens. */
function guestKind(
  type: Exclude<ConsoleType, "node_shell">,
): ConsoleResourceKind {
  return type === "ct_vnc" || type === "ct_attach" ? "ct" : "vm";
}

/** The console:* resource a console type needs, as the launchers check it. */
export function consolePermission(
  type: ConsoleType,
): "node" | "vm" | "container" {
  if (type === "node_shell") return "node";
  return guestKind(type) === "ct" ? "container" : "vm";
}

/**
 * The tab label for a console, `<Shell|VNC|Serial|CT>: <name>`: every launcher's
 * and the console page's. A node shell is named by its node; a guest by its
 * name once known, by its VMID until then.
 */
export function consoleLabel(
  target: Pick<ConsoleTarget, "type" | "node" | "vmid">,
  guestName?: string,
): string {
  if (target.type === "node_shell") return `Shell: ${target.node}`;
  const name =
    guestName !== undefined && guestName !== ""
      ? guestName
      : String(target.vmid ?? "");
  return `${LABEL_PREFIX[target.type]}: ${name}`;
}

/**
 * The console page's address for `target`. The label stays out of it: the page
 * names the console from the server's data, once it has checked that the guest
 * the link names is the one it dials (ConsoleSessionPage).
 */
export function consoleSessionHref(target: ConsoleTarget): string {
  const params = new URLSearchParams({
    cluster: target.clusterID,
    type: target.type,
    node: target.node,
  });
  if (target.type !== "node_shell" && target.vmid !== undefined) {
    params.set("vmid", String(target.vmid));
    if (target.resourceId !== undefined) {
      params.set("resource", target.resourceId);
    }
  }
  return `${CONSOLE_SESSION_PATH}?${params.toString()}`;
}

/**
 * The console a console page's query names, or null when it names none. Only
 * the shape is checked here — a known type, a cluster and node, a whole VMID
 * for a guest. Whether the node and the guest are the cluster's is the page's
 * to check, and whether the user may open it the server's, at the token mint.
 */
export function parseConsoleSessionTarget(
  params: URLSearchParams,
): ConsoleTarget | null {
  const type = params.get("type");
  const clusterID = params.get("cluster") ?? "";
  const node = params.get("node") ?? "";
  if (!isConsoleType(type) || clusterID === "" || node === "") return null;

  if (type === "node_shell") {
    const target: ConsoleTarget = { clusterID, node, type, label: "" };
    return { ...target, label: consoleLabel(target) };
  }

  const rawVmid = params.get("vmid") ?? "";
  if (!/^[1-9]\d*$/.test(rawVmid)) return null;
  const vmid = Number(rawVmid);
  if (!Number.isSafeInteger(vmid)) return null;

  const resourceId = params.get("resource") ?? "";
  const target: ConsoleTarget = {
    clusterID,
    node,
    type,
    vmid,
    label: "",
    ...(resourceId !== "" ? { resourceId, kind: guestKind(type) } : {}),
  };
  return { ...target, label: consoleLabel(target) };
}

/** The modifier keys of the click that asked for a console. */
export interface ConsoleClickKeys {
  ctrlKey: boolean;
  metaKey: boolean;
}

/**
 * Whether a console asked for with `keys` opens in a browser tab of its own.
 * Ctrl/⌘-click always does, as it opens a link in a new tab; otherwise the
 * preference decides, and anything but "tab" — a value this build does not
 * know, from storage or the server — keeps the floating window.
 */
export function wantsNewTab(mode: string, keys: ConsoleClickKeys): boolean {
  return keys.ctrlKey || keys.metaKey || mode === "tab";
}
