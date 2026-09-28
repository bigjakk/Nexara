import type { UseQueryResult } from "@tanstack/react-query";
import { describeError } from "@/lib/api-error";
import type { MappingUsage } from "../api/mapping-queries";
import { cleanDeviceText } from "../lib/usb-mapping";

/**
 * A mapping's delete confirmation text: what goes, and which VMs use the
 * mapping. `note`, when set, comes first — why a Remove became a Delete, say.
 * Inline elements only — the dialog renders this inside a paragraph.
 */
export function MappingDeleteDescription({
  kind,
  mappingId,
  entries,
  note,
  usage,
}: {
  /** The kind, as the text names it. */
  kind: "USB" | "PCI";
  mappingId: string;
  /** How many node entries the mapping has. */
  entries: number;
  note?: string | undefined;
  usage: UseQueryResult<MappingUsage>;
}) {
  return (
    <>
      {note !== undefined && <span className="block">{note}</span>}
      <span className="block">
        Deletes the {kind} mapping {mappingId} and its{" "}
        {entries === 1 ? "entry" : `${String(entries)} entries`} from the
        cluster. Proxmox does not check whether a VM uses it: one that does will
        not start until a mapping named {mappingId} exists again.
      </span>
      <UsageSummary usage={usage} />
    </>
  );
}

function UsageChecking() {
  return (
    <span className="mt-2 block">
      Checking which VMs use it… The delete is on offer once the check has
      answered.
    </span>
  );
}

function guestName(g: { vmid: number; name: string; node: string }): string {
  const name = cleanDeviceText(g.name);
  return `${String(g.vmid)}${name ? ` (${name})` : ""} on ${g.node}`;
}

/** Which VMs use the mapping, as the usage check answered — or why it could not. */
function UsageSummary({ usage }: { usage: UseQueryResult<MappingUsage> }) {
  if (usage.isFetching) {
    return <UsageChecking />;
  }
  if (usage.isError) {
    const why = describeError(usage.error);
    return (
      <span className="mt-2 block font-medium text-amber-700 dark:text-amber-400">
        Could not check which VMs use it{why ? `: ${why}` : "."} Any VM that
        does will not start after the delete.
      </span>
    );
  }
  if (!usage.isSuccess) {
    return <UsageChecking />;
  }
  const { users, unchecked } = usage.data;
  return (
    <>
      {users.length > 0 ? (
        <span className="mt-2 block font-medium text-destructive">
          {users.length === 1
            ? "1 VM uses it and will not start after the delete:"
            : `${String(users.length)} VMs use it and will not start after the delete:`}
          {users.map((g) => (
            <span key={g.vmid} className="block pl-3 font-normal">
              {guestName(g)} — {(g.keys ?? []).join(", ")}
            </span>
          ))}
        </span>
      ) : (
        <span className="mt-2 block">
          {unchecked.length > 0
            ? "No VM that could be checked uses it."
            : "No VM's current configuration uses it."}
        </span>
      )}
      {unchecked.length > 0 && (
        <span className="mt-2 block text-amber-700 dark:text-amber-400">
          {unchecked.length === 1
            ? "1 VM could not be checked and may use it:"
            : `${String(unchecked.length)} VMs could not be checked and may use it:`}
          {unchecked.map((g) => (
            <span key={g.vmid} className="block pl-3">
              {guestName(g)} — {cleanDeviceText(g.reason ?? "")}
            </span>
          ))}
        </span>
      )}
    </>
  );
}
