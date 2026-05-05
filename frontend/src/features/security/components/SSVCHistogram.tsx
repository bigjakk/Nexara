import { Zap, Clock, Eye, FileText } from "lucide-react";
import { cn } from "@/lib/utils";
import type { SSVCLabel } from "@/types/api";

/**
 * SSVC (Stakeholder-Specific Vulnerability Categorization) action histogram.
 *
 * Adapted from CISA's decision tree (https://www.cisa.gov/ssvc-calculator).
 * Tells the user what to *do* about each vulnerability rather than what
 * its severity score is, since a single posture number can't distinguish
 * "100 routine pending updates" from "1 actively-exploited critical".
 */

interface SSVCHistogramProps {
  actCount: number;
  attendCount: number;
  trackStarCount: number;
  trackCount: number;
}

interface ActionConfig {
  label: string;
  icon: typeof Zap;
  description: string;
  cardClass: string;
  iconClass: string;
  countClass: string;
}

const actionConfig: Record<SSVCLabel, ActionConfig> = {
  act: {
    label: "Act",
    icon: Zap,
    description: "Patch immediately",
    cardClass: "border-red-500/40 bg-red-500/10",
    iconClass: "text-red-500",
    countClass: "text-red-700 dark:text-red-400",
  },
  attend: {
    label: "Attend",
    icon: Clock,
    description: "Plan this week",
    cardClass: "border-orange-500/40 bg-orange-500/10",
    iconClass: "text-orange-500",
    countClass: "text-orange-700 dark:text-orange-400",
  },
  track_star: {
    label: "Track*",
    icon: Eye,
    description: "Next routine cycle",
    cardClass: "border-yellow-500/40 bg-yellow-500/10",
    iconClass: "text-yellow-500",
    countClass: "text-yellow-700 dark:text-yellow-500",
  },
  track: {
    label: "Track",
    icon: FileText,
    description: "Routine maintenance",
    cardClass: "border-muted bg-muted/30",
    iconClass: "text-muted-foreground",
    countClass: "text-foreground",
  },
};

export function SSVCHistogram({
  actCount,
  attendCount,
  trackStarCount,
  trackCount,
}: SSVCHistogramProps) {
  const total = actCount + attendCount + trackStarCount + trackCount;
  if (total === 0) return null;

  const items: Array<{ label: SSVCLabel; count: number }> = [
    { label: "act", count: actCount },
    { label: "attend", count: attendCount },
    { label: "track_star", count: trackStarCount },
    { label: "track", count: trackCount },
  ];

  return (
    <div className="rounded-lg border bg-card p-6">
      <div className="mb-3 flex items-baseline justify-between">
        <h3 className="text-sm font-medium text-muted-foreground">
          Action Required (SSVC)
        </h3>
        <span
          className="text-xs text-muted-foreground"
          title="CISA Stakeholder-Specific Vulnerability Categorization"
        >
          per CISA decision tree
        </span>
      </div>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {items.map(({ label, count }) => {
          const cfg = actionConfig[label];
          const Icon = cfg.icon;
          return (
            <div
              key={label}
              className={cn(
                "flex flex-col items-start gap-1 rounded-md border p-3",
                cfg.cardClass,
              )}
              title={cfg.description}
            >
              <div className="flex items-center gap-1.5">
                <Icon className={cn("h-4 w-4", cfg.iconClass)} />
                <span className="text-xs font-semibold uppercase tracking-wide">
                  {cfg.label}
                </span>
              </div>
              <span className={cn("text-2xl font-bold", cfg.countClass)}>
                {count}
              </span>
              <span className="text-[11px] text-muted-foreground">
                {cfg.description}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
