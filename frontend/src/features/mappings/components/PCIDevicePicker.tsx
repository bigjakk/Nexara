import type { NodePCIDevice } from "@/features/vms/api/vm-queries";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { PCIPick } from "../hooks/usePCIPick";
import { pciDeviceLabel } from "../lib/pci-mapping";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

/**
 * Picks a host PCI device (usePCIPick): the node's devices grouped by IOMMU
 * group, as Proxmox lists them — a device shares its group with whatever else
 * is in it — or, when the node lists none, a typed address; and All functions,
 * which passes the whole device through as one. The controls' ids start with
 * `idPrefix`. `isTaken` greys out a device the caller will not take — one the
 * mapping already has on the node, say — for display only: a pick made before
 * it was taken, ticking All functions after it, or a typed address is not
 * checked, so the caller refuses a taken pick itself. `onPicked` runs after any
 * change, to clear a refusal the old pick earned.
 */
export function PCIDevicePicker({
  idPrefix,
  pick,
  disabled,
  onPicked,
  isTaken,
}: {
  idPrefix: string;
  pick: PCIPick;
  disabled: boolean;
  onPicked?: (() => void) | undefined;
  isTaken?: ((device: NodePCIDevice) => boolean) | undefined;
}) {
  const grouped = new Map<number, NodePCIDevice[]>();
  for (const d of pick.deviceList) {
    const list = grouped.get(d.iommugroup) ?? [];
    list.push(d);
    grouped.set(d.iommugroup, list);
  }

  return (
    <div className="space-y-1">
      <Label htmlFor={`${idPrefix}-device`} className="text-xs">
        Device
      </Label>
      {pick.hasDeviceList ? (
        <select
          id={`${idPrefix}-device`}
          className={selectClass}
          value={pick.address}
          disabled={disabled}
          onChange={(e) => {
            pick.setAddress(e.target.value);
            onPicked?.();
          }}
        >
          <option value="">Select a device...</option>
          {Array.from(grouped.entries())
            .sort(([a], [b]) => a - b)
            .map(([group, devs]) => (
              <optgroup
                key={group}
                label={
                  group < 0 ? "No IOMMU group" : `IOMMU Group ${String(group)}`
                }
              >
                {devs.map((d) => (
                  <option
                    key={d.id}
                    value={d.id}
                    disabled={isTaken?.(d) ?? false}
                  >
                    {d.id} — {pciDeviceLabel(d)}
                  </option>
                ))}
              </optgroup>
            ))}
        </select>
      ) : (
        <Input
          id={`${idPrefix}-device`}
          value={pick.address}
          disabled={disabled}
          onChange={(e) => {
            pick.setAddress(e.target.value);
            onPicked?.();
          }}
          placeholder="PCI address, e.g. 0000:01:00.0"
          aria-invalid={pick.typedAddressError !== ""}
        />
      )}
      {pick.typedAddressError && (
        <p className="text-xs text-destructive">{pick.typedAddressError}</p>
      )}
      <div className="flex items-center gap-1.5 pt-1">
        <Checkbox
          id={`${idPrefix}-all-functions`}
          checked={pick.allFunctions}
          disabled={disabled}
          onCheckedChange={(v) => {
            pick.setAllFunctions(v === true);
            onPicked?.();
          }}
        />
        <Label
          htmlFor={`${idPrefix}-all-functions`}
          className="cursor-pointer text-xs"
        >
          All functions
          <span className="ml-1.5 text-muted-foreground">
            Pass the whole device through, every function as one
          </span>
        </Label>
      </div>
    </div>
  );
}
