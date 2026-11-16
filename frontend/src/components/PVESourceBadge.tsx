import { Monitor } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The pill that marks a row as ingested from Proxmox rather than raised by
 * Nexara, or as filed through POST /api/v1/tasks (source "filed", until
 * whoever ran the task takes the row over) with who filed it. Renders nothing
 * for any other source, so the "which source counts" rule lives here and not
 * at each call site.
 */
export function PVESourceBadge({
  source,
  filedBy,
}: {
  source: string;
  /** Who filed a "filed" row. */
  filedBy?: string | undefined;
}) {
  const { t } = useTranslation("common");
  if (source === "filed") {
    const label = filedBy ? t("filedBy", { user: filedBy }) : t("filed");
    return (
      <span
        title={label}
        className="inline-block max-w-[14rem] shrink-0 truncate rounded-[5px] bg-foreground/[0.07] px-1.5 py-[3px] text-[11px] leading-none font-medium text-muted-foreground"
      >
        {label}
      </span>
    );
  }
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
