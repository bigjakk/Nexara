import type { USBMapping } from "../api/mapping-queries";
import { usbMappingEntryFor } from "../lib/usb-mapping";

/**
 * What Proxmox reported for a mapping against the VM's node, or, when it
 * reported nothing, what the mapping passes through there.
 */
export function MappingCheckList({
  mapping,
  node,
}: {
  mapping: USBMapping;
  node: string;
}) {
  if (mapping.errors.length === 0) {
    const entry = usbMappingEntryFor(mapping.map, node);
    if (!entry) return null;
    return (
      <p className="text-xs text-muted-foreground">
        On {node}: {entry.id}
        {entry.path ? ` on port ${entry.path}` : ""}
      </p>
    );
  }
  return (
    <ul className="space-y-0.5">
      {mapping.errors.map((e) => (
        <li
          key={`${e.severity}:${e.message}`}
          className={
            e.severity === "error"
              ? "text-xs text-destructive"
              : "text-xs text-amber-700 dark:text-amber-400"
          }
        >
          {e.message}
        </li>
      ))}
    </ul>
  );
}
