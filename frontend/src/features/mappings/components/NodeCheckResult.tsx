import { CheckCircle2 } from "lucide-react";
import type { MappingCheck } from "../api/mapping-queries";
import { ownValue } from "../lib/mapping-edits";
import { cleanDeviceText } from "../lib/usb-mapping";

/**
 * What a cluster-wide listing says about a mapping on `node`: why the node was
 * not checked, the warnings and errors Proxmox reported checking it there, or
 * — a clean check — OK. A node that was not checked is never shown as OK.
 * `showOK` false leaves a clean check blank, for a caller with a problem of its
 * own to state instead.
 *
 * `unchecked` and `checks` are the listing's two maps, read here by the node's
 * OWN key (ownValue).
 */
export function NodeCheckResult({
  node,
  unchecked,
  checks,
  showOK = true,
}: {
  node: string;
  /** Why each node was not checked, for the nodes that were not. */
  unchecked: Readonly<Record<string, string>>;
  /** What Proxmox reported checking each node, for the nodes that were. */
  checks: Readonly<Record<string, readonly MappingCheck[]>>;
  showOK?: boolean;
}) {
  const reason = ownValue(unchecked, node);
  const reported = ownValue(checks, node);
  if (reason !== undefined) {
    return (
      <p className="text-xs text-muted-foreground">
        Not checked: {cleanDeviceText(reason)}
      </p>
    );
  }
  if (reported === undefined) {
    return <p className="text-xs text-muted-foreground">Not checked.</p>;
  }
  if (reported.length > 0) {
    return (
      <ul className="space-y-0.5">
        {reported.map((c, i) => (
          <li
            // By position too: a PCI mapping's entries on one node can draw
            // the same message each.
            key={`${String(i)}:${c.severity}:${c.message}`}
            className={
              c.severity === "error"
                ? "text-xs text-destructive"
                : "text-xs text-amber-700 dark:text-amber-400"
            }
          >
            {cleanDeviceText(c.message)}
          </li>
        ))}
      </ul>
    );
  }
  if (!showOK) return null;
  return (
    <span className="inline-flex items-center gap-1 text-xs text-emerald-700 dark:text-emerald-400">
      <CheckCircle2 className="h-3.5 w-3.5" />
      OK
    </span>
  );
}
