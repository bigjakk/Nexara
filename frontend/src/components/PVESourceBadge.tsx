import { Monitor } from "lucide-react";

/**
 * The pill that marks a row as ingested from Proxmox rather than raised by
 * Nexara. Renders nothing for any other source, so the "which source counts"
 * rule lives here and not at each call site.
 */
export function PVESourceBadge({ source }: { source: string }) {
  if (source !== "proxmox") return null;
  return (
    <span className="inline-flex shrink-0 items-center gap-0.5 rounded-full bg-orange-500/10 px-1.5 py-0.5 text-[10px] leading-none font-medium text-orange-600 dark:text-orange-400">
      <Monitor className="h-2.5 w-2.5" />
      PVE
    </span>
  );
}
