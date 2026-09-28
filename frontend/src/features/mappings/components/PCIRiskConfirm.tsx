import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";

/**
 * Asks before a device the node may need is passed through: when a VM starts
 * with it, Proxmox takes the device — and its IOMMU group — away from the
 * node, whether or not the node is using it. Asked, not stopped: passing a
 * spare disk controller or NIC through is what the dialogs are for. Renders
 * nothing when there is no risk to state. The checkbox's id is
 * `${idPrefix}-host-risk`.
 */
export function PCIRiskConfirm({
  idPrefix,
  node,
  risks,
  accepted,
  onAcceptedChange,
  disabled,
}: {
  idPrefix: string;
  node: string;
  risks: readonly string[];
  accepted: boolean;
  onAcceptedChange: (accepted: boolean) => void;
  disabled: boolean;
}) {
  if (risks.length === 0) return null;
  return (
    <div className="space-y-1.5 rounded-md border border-amber-300 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-300">
      <p>
        When the VM starts, the device is taken away from {node} — check that
        the node does not need it:
      </p>
      <ul className="list-disc space-y-0.5 pl-4">
        {risks.map((r, i) => (
          // By position too: nothing here promises the caller's warnings
          // are distinct.
          <li key={`${String(i)}:${r}`}>{r}</li>
        ))}
      </ul>
      <div className="flex items-center gap-1.5">
        <Checkbox
          id={`${idPrefix}-host-risk`}
          checked={accepted}
          disabled={disabled}
          onCheckedChange={(v) => {
            onAcceptedChange(v === true);
          }}
        />
        <Label
          htmlFor={`${idPrefix}-host-risk`}
          className="cursor-pointer text-xs"
        >
          The node does not need it; pass it through
        </Label>
      </div>
    </div>
  );
}
