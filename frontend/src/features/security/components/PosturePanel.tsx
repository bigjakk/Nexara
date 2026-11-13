import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import { Button } from "@/components/ui/button";
import type { SecurityPosture } from "@/types/api";
import { countTone, hasScans, scoreTone } from "../lib/posture";

interface PosturePanelProps {
  posture: SecurityPosture;
  /** Optional handler for the "actively exploited" KEV line's button.
   *  Receives the scan_id to deep-link into the filtered vuln view. */
  onShowKEV?: (scanId: string) => void;
}

/**
 * What the newest completed scan found: the score, how much of the cluster it
 * covered, and the findings by severity. A zero is quiet; only a number that
 * is a problem takes a colour.
 */
export function PosturePanel({ posture, onShowKEV }: PosturePanelProps) {
  if (!hasScans(posture)) {
    return (
      <Panel title="Posture">
        <p className="text-sm text-muted-foreground">
          No scans have been run yet. Use Scan now to check this cluster&apos;s
          nodes for known vulnerabilities.
        </p>
      </Panel>
    );
  }

  const scannedAt = posture.completed_at ?? posture.started_at;
  const partial = posture.scanned_nodes < posture.total_nodes;

  return (
    <Panel
      flush
      title="Posture"
      aside={
        scannedAt
          ? `from the scan of ${new Date(scannedAt).toLocaleString()}`
          : undefined
      }
    >
      <StatStrip>
        <Stat
          label="Posture score"
          value={String(Math.round(posture.posture_score))}
          sub="of 100"
          tone={scoreTone(posture.posture_score)}
        />
        <Stat
          label="Nodes scanned"
          value={`${String(posture.scanned_nodes)} of ${String(posture.total_nodes)}`}
          sub={partial ? "the rest were not reached" : undefined}
          tone={partial ? "warn" : "default"}
        />
        <Stat
          label="Critical"
          value={String(posture.critical_count)}
          tone={countTone(posture.critical_count, "bad")}
        />
        <Stat
          label="High"
          value={String(posture.high_count)}
          tone={countTone(posture.high_count, "warn")}
        />
        <Stat
          label="Medium"
          value={String(posture.medium_count)}
          tone={countTone(posture.medium_count, "default")}
        />
        <Stat
          label="Low"
          value={String(posture.low_count)}
          tone={countTone(posture.low_count, "default")}
        />
        {posture.unknown_count > 0 && (
          <Stat label="Unknown" value={String(posture.unknown_count)} />
        )}
      </StatStrip>

      {posture.kev_count > 0 && (
        <div className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 border-t px-[18px] pt-3.5">
          <span
            aria-hidden="true"
            className="h-2 w-2 shrink-0 rounded-full bg-red-500"
          />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-red-700 dark:text-red-400">
              {posture.kev_count} actively exploited{" "}
              {posture.kev_count === 1 ? "vulnerability" : "vulnerabilities"}
            </p>
            <p className="text-[12.5px] text-muted-foreground">
              Listed in CISA&apos;s Known Exploited Vulnerabilities catalog.
              Patch these first.
            </p>
          </div>
          {onShowKEV && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                onShowKEV(posture.scan_id);
              }}
            >
              Review
            </Button>
          )}
        </div>
      )}
    </Panel>
  );
}
