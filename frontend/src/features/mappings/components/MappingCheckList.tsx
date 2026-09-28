import type {
  MappingCheck,
  PCIMapping,
  USBMapping,
} from "../api/mapping-queries";
import { usbMappingEntryFor } from "../lib/usb-mapping";
import { parsePCIMappingEntry } from "../lib/pci-mapping";

/** Proxmox's check of a mapping against a node: its warnings and errors. */
function MappingChecks({ checks }: { checks: readonly MappingCheck[] }) {
  return (
    <ul className="space-y-0.5">
      {checks.map((e, i) => (
        <li
          // By position too: a PCI mapping's entries on one node can draw
          // the same message each.
          key={`${String(i)}:${e.severity}:${e.message}`}
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
  return <MappingChecks checks={mapping.errors} />;
}

/**
 * MappingCheckList for a PCI mapping, whose listing calls the checks
 * `checks`, and which may have several entries for the node: a VM starting
 * there takes the first device not in use.
 */
export function PCIMappingCheckList({
  mapping,
  node,
}: {
  mapping: PCIMapping;
  node: string;
}) {
  if (mapping.checks.length === 0) {
    const paths = mapping.map
      .map(parsePCIMappingEntry)
      .filter((e) => e.node === node)
      .map((e) => e.path);
    if (paths.length === 0) return null;
    return (
      <p className="text-xs text-muted-foreground">
        On {node}: {paths.join(", ")}
        {paths.length > 1 ? " — the first one not in use when the VM starts" : ""}
      </p>
    );
  }
  return <MappingChecks checks={mapping.checks} />;
}
