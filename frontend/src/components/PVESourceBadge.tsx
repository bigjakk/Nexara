import { Monitor } from "lucide-react";

/**
 * The pill that marks a row as ingested from Proxmox rather than raised by
 * Nexara. Renders nothing for any other source, so the "which source counts"
 * rule lives here and not at each call site.
 */
export function PVESourceBadge({ source }: { source: string }) {
  if (source !== "proxmox") return null;
  return (
    // A label, not a state: neutral, so the row keeps one colour to read.
    <span
      title="Ingested from Proxmox"
      className="inline-flex shrink-0 items-center gap-1 rounded-[5px] bg-foreground/[0.07] px-1.5 py-[3px] text-[11px] leading-none font-medium text-muted-foreground"
    >
      <Monitor className="h-2.5 w-2.5" />
      PVE
    </span>
  );
}
