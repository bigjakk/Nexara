import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import type { AlertSummary } from "@/types/api";

/** "1 critical, 2 warning": the severities among what is firing. */
function severities(summary: AlertSummary): string | undefined {
  const parts: string[] = [];
  if (summary.critical_firing > 0) {
    parts.push(`${String(summary.critical_firing)} critical`);
  }
  if (summary.warning_firing > 0) {
    parts.push(`${String(summary.warning_firing)} warning`);
  }
  if (summary.info_firing > 0) {
    parts.push(`${String(summary.info_firing)} info`);
  }
  return parts.length > 0 ? parts.join(", ") : undefined;
}

/**
 * The alert counts as one strip. A zero wears no colour: only alerts that
 * exist draw the eye, red for firing and amber for pending.
 */
export function AlertSummaryStrip({
  summary,
}: {
  summary: AlertSummary | undefined;
}) {
  const firing = summary?.firing_count ?? 0;
  const pending = summary?.pending_count ?? 0;
  const acknowledged = summary?.acknowledged_count ?? 0;
  const active = firing + pending + acknowledged;
  const count = (n: number) => (summary === undefined ? "—" : String(n));

  return (
    <Panel flush bodyClassName="py-4" aria-label="Alert summary">
      <StatStrip>
        <Stat
          label="Firing"
          value={count(firing)}
          sub={summary === undefined ? undefined : severities(summary)}
          tone={firing > 0 ? "bad" : "quiet"}
        />
        <Stat
          label="Pending"
          value={count(pending)}
          tone={pending > 0 ? "warn" : "quiet"}
        />
        <Stat
          label="Acknowledged"
          value={count(acknowledged)}
          tone={acknowledged > 0 ? "default" : "quiet"}
        />
        <Stat
          label="Total active"
          value={count(active)}
          tone={active > 0 ? "default" : "quiet"}
        />
      </StatStrip>
    </Panel>
  );
}
