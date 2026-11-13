import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip, type StatTone } from "@/components/page/StatStrip";
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
  description: string;
  /** The tone once there is at least one; zero is always quiet. */
  tone: StatTone;
}

const actionConfig: Record<SSVCLabel, ActionConfig> = {
  act: { label: "Act", description: "Patch immediately", tone: "bad" },
  attend: { label: "Attend", description: "Plan this week", tone: "warn" },
  track_star: {
    label: "Track*",
    description: "Next routine cycle",
    tone: "default",
  },
  track: {
    label: "Track",
    description: "Routine maintenance",
    tone: "default",
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
    <Panel
      flush
      title="What to do"
      aside={
        <span title="CISA Stakeholder-Specific Vulnerability Categorization">
          each finding sorted by CISA&apos;s SSVC decision tree
        </span>
      }
    >
      <StatStrip>
        {items.map(({ label, count }) => {
          const cfg = actionConfig[label];
          return (
            <Stat
              key={label}
              label={cfg.label}
              value={String(count)}
              sub={cfg.description}
              tone={count > 0 ? cfg.tone : "quiet"}
            />
          );
        })}
      </StatStrip>
    </Panel>
  );
}
