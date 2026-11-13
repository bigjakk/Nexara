import { Badge, type BadgeVariant } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { UpsDevice, UpsFeed } from "../types/ups";
import { protectionView, type ProtectionTone } from "../lib/ups-feeds";
import { upsFlagChips, upsStatus, type UpsLabel, type UpsTone } from "../lib/ups-state";

const TONE: Record<UpsTone, BadgeVariant> = {
  ok: "healthy",
  warn: "warning",
  err: "destructive",
  idle: "secondary",
};

export function UpsToneBadge({ label, className }: { label: UpsLabel; className?: string }) {
  return (
    <Badge variant={TONE[label.tone]} className={className}>
      {label.label}
    </Badge>
  );
}

/** The flags the status does not already say, for a UPS read just now. */
function liveFlags(device: UpsDevice): UpsLabel[] {
  return device.reachability === "ok" && !device.reading_stale ? upsFlagChips(device) : [];
}

/** The status chip, then a chip per flag the status does not already say. */
export function UpsStatusBadges({ device }: { device: UpsDevice }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <UpsToneBadge label={upsStatus(device)} />
      {liveFlags(device).map((chip) => (
        <UpsToneBadge key={chip.label} label={chip} className="font-medium" />
      ))}
    </div>
  );
}

/**
 * The same in words, for a place that already draws the state as a dot: a
 * chip only for what is wrong, plain text for the rest.
 */
export function UpsStatusText({ device, className }: { device: UpsDevice; className?: string }) {
  const status = upsStatus(device);
  const quiet = (l: UpsLabel) => l.tone === "ok" || l.tone === "idle";
  return (
    <span className={cn("flex flex-wrap items-center gap-x-2 gap-y-1", className)}>
      {quiet(status) ? (
        <span className={cn("text-sm", status.tone === "idle" && "text-muted-foreground")}>{status.label}</span>
      ) : (
        <UpsToneBadge label={status} />
      )}
      {liveFlags(device).map((chip) =>
        quiet(chip) ? (
          <span key={chip.label} className="text-[12.5px] text-muted-foreground">
            {chip.label}
          </span>
        ) : (
          <UpsToneBadge key={chip.label} label={chip} className="font-medium" />
        ),
      )}
    </span>
  );
}

const PROTECTION: Record<ProtectionTone, { variant: BadgeVariant; className?: string }> = {
  ok: { variant: "healthy" },
  warn: { variant: "warning" },
  muted: { variant: "outline", className: "font-medium text-muted-foreground" },
};

/** A feed's verdict as a chip, with what it rests on as its title. */
export function ProtectionBadge({ feed, className }: { feed: UpsFeed; className?: string }) {
  const v = protectionView(feed);
  const t = PROTECTION[v.tone];
  return (
    <Badge variant={t.variant} className={cn(t.className, className)} title={v.hint}>
      {v.label}
    </Badge>
  );
}
