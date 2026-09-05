import { Badge } from "@/components/ui/badge";
import { classifyOS } from "@/lib/os-classify";
import { useGuestToolsGuest } from "../api/guest-tools-queries";
import { guestToolsState } from "../lib/guest-tools-state";

interface VMGuestToolsSummaryProps {
  clusterId: string;
  vmid: number;
  configOstype: string;
  ostype: string;
}

/**
 * One-line guest tools status for the QEMU Guest Agent panel.
 *
 * Read-only by design: the point is that an operator looking at the agent panel
 * can see at a glance whether an update is waiting, without the Overview tab
 * growing a second block of controls. Everything actionable lives on the Guest
 * Tools tab.
 *
 * Renders nothing for non-Windows guests, or before anything is known — an
 * empty slot is better than a row that says "Unknown" on every Linux VM.
 */
export function VMGuestToolsSummary({
  clusterId,
  vmid,
  configOstype,
  ostype,
}: VMGuestToolsSummaryProps) {
  const isWindows =
    classifyOS(configOstype) === "windows" || classifyOS(ostype) === "windows";
  // Hook called unconditionally (rules of hooks), but gated so a Linux guest
  // never triggers the request.
  const { guest } = useGuestToolsGuest(clusterId, vmid, isWindows);
  if (!isWindows || !guest) return null;

  const { label, variant } = guestToolsState(guest);
  return (
    <div>
      <p className="text-xs text-muted-foreground">Guest Tools</p>
      <div className="flex items-center gap-2">
        <p className="text-sm">{guest.installed_version || "Not installed"}</p>
        <Badge variant={variant}>{label}</Badge>
      </div>
    </div>
  );
}
