import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { UpsDevice } from "../types/ups";
import { upsFlagChips, upsStatus, type UpsLabel, type UpsTone } from "../lib/ups-state";

const TONE: Record<UpsTone, { variant: "default" | "secondary" | "destructive"; className?: string }> = {
  ok: { variant: "default", className: "bg-emerald-600 hover:bg-emerald-600" },
  warn: { variant: "default", className: "bg-amber-500 text-amber-950 hover:bg-amber-500" },
  err: { variant: "destructive" },
  idle: { variant: "secondary" },
};

export function UpsToneBadge({ label, className }: { label: UpsLabel; className?: string }) {
  const t = TONE[label.tone];
  return (
    <Badge variant={t.variant} className={cn(t.className, className)}>
      {label.label}
    </Badge>
  );
}

/** The status chip, then a chip per flag the status does not already say. */
export function UpsStatusBadges({ device }: { device: UpsDevice }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <UpsToneBadge label={upsStatus(device)} />
      {device.reachability === "ok" &&
        !device.reading_stale &&
        upsFlagChips(device).map((chip) => (
          <UpsToneBadge key={chip.label} label={chip} className="font-medium" />
        ))}
    </div>
  );
}
